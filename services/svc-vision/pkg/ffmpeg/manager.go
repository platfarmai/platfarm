package ffmpeg

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/sirupsen/logrus"
)

type Manager struct {
	streams map[uint]*Stream
	mu      sync.RWMutex
}

type StreamOptions struct {
	CameraID        uint
	SegmentDuration int
	OutputDir       string
	OnSegment       func(cameraID uint, segment *SegmentInfo)
	OnError         func(error)
	// OnSegmentStart 在新片段的 ffmpeg 进程确认正常运行（真正连上流源）时调用
	OnSegmentStart func()

	NoRecord bool

	RecordOnly bool
}

type Stream struct {
	opts       StreamOptions
	rtspURL    string
	cmd        *exec.Cmd
	ctx        context.Context
	cancel     context.CancelFunc
	doneChan   chan struct{}
	running    bool
	mu         sync.Mutex
	startTime  time.Time
	segmentIdx int
	stderrTail bytes.Buffer
	stderrMu   sync.Mutex

	runStartTime  time.Time
	runCounter    int
	processedSegs map[string]bool
	processedMu   sync.Mutex
	csvWatcher    chan struct{}

	segExited bool // 当前片段的 ffmpeg 进程已退出（受 mu 保护）
	segRunID  int  // 每个片段自增，用于作废旧片段残留的存活检查
	live      bool // 当前片段已确认正常运行（受 mu 保护）

	restartRequested bool
}

type SegmentInfo struct {
	Index     int
	StartTime time.Time
	EndTime   time.Time
	FilePath  string
	FileSize  int64
	IndexPath string
	Duration  float64
}

func NewManager() *Manager {
	return &Manager{
		streams: make(map[uint]*Stream),
	}
}

func (m *Manager) CreateStream(rtspURL string, opts StreamOptions) (*Stream, error) {

	if err := os.MkdirAll(opts.OutputDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create output directory: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	s := &Stream{
		opts:          opts,
		rtspURL:       rtspURL,
		ctx:           ctx,
		cancel:        cancel,
		doneChan:      make(chan struct{}),
		segmentIdx:    0,
		processedSegs: make(map[string]bool),
	}

	m.mu.Lock()
	m.streams[opts.CameraID] = s
	m.mu.Unlock()

	return s, nil
}

func (s *Stream) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.running {
		return fmt.Errorf("stream already running")
	}

	s.startTime = time.Now()
	s.running = true

	go s.run()
	return nil
}

func (s *Stream) run() {
	defer func() {
		s.mu.Lock()
		s.running = false
		s.live = false
		close(s.doneChan)
		s.mu.Unlock()
	}()

	for {
		select {
		case <-s.ctx.Done():
			return
		default:
			if err := s.runSegment(); err != nil {
				if s.ctx.Err() == context.Canceled {
					return
				}
				s.mu.Lock()
				s.live = false
				s.mu.Unlock()
				logrus.Errorf("camera %d recording segment error: %v", s.opts.CameraID, err)
				if s.opts.OnError != nil {
					s.opts.OnError(err)
				}

				select {
				case <-s.ctx.Done():
					return
				case <-time.After(2 * time.Second):
					continue
				}
			}
		}
	}
}

func (s *Stream) runSegment() error {
	segmentStart := time.Now()
	s.runStartTime = segmentStart

	s.runCounter++
	runPrefix := "segment_" + segmentStart.Format("20060102_150405") + "_" + fmt.Sprintf("%02d", s.runCounter)

	segmentTemplate := filepath.Join(s.opts.OutputDir, runPrefix+"_%06d.mp4")
	indexFile := filepath.Join(s.opts.OutputDir, runPrefix+".idx")
	hlsSegmentFile := filepath.Join(s.opts.OutputDir, "hls_segment_%03d.ts")
	hlsPlaylist := filepath.Join(s.opts.OutputDir, "index.m3u8")

	args := []string{
		"-y",
		"-rtsp_transport", "tcp",

		"-i", "INPUT_URL_PLACEHOLDER",
	}

	if !s.opts.NoRecord {

		args = append(args,
			"-map", "0:v:0",
			"-c", "copy",
			"-movflags", "+faststart",
			"-f", "segment",
			"-segment_time", strconv.Itoa(s.opts.SegmentDuration),
			"-segment_format", "mp4",
			"-reset_timestamps", "1",
			"-segment_list", indexFile,
			"-segment_list_type", "csv",
			"-segment_list_entry_prefix", "",
			"-start_number", "0",
			segmentTemplate,
		)
	}

	if !s.opts.RecordOnly {
		args = append(args,
			"-map", "0:v:0",
			"-c:v", "libx264",
			"-preset", "veryfast",
			"-tune", "zerolatency",
			"-g", "50",
			"-keyint_min", "50",
			"-sc_threshold", "0",
			"-pix_fmt", "yuv420p",
			"-an",
			"-f", "hls",
			"-hls_time", "2",
			"-hls_list_size", "6",
			"-hls_flags", "delete_segments+append_list",
			"-hls_segment_filename", hlsSegmentFile,
			hlsPlaylist,
		)
	}

	for i, arg := range args {
		if arg == "INPUT_URL_PLACEHOLDER" {
			args[i] = s.getRTSPURL()
			break
		}
	}

	s.mu.Lock()
	s.cmd = exec.CommandContext(s.ctx, "ffmpeg", args...)
	s.segExited = false
	s.segRunID++
	runID := s.segRunID
	s.mu.Unlock()

	stderr, _ := s.cmd.StderrPipe()
	stdout, _ := s.cmd.StdoutPipe()

	if err := s.cmd.Start(); err != nil {
		return fmt.Errorf("failed to start ffmpeg: %w", err)
	}

	go s.readOutput(stderr, "stderr")
	go s.readOutput(stdout, "stdout")

	s.startCSVWatcher(indexFile)

	// 存活确认：启动 5 秒后进程仍存活，视为真正连上流源
	// （流源不可达时 ffmpeg 通常 1~2 秒内即报错退出）
	go func() {
		time.Sleep(5 * time.Second)
		s.mu.Lock()
		ok := s.running && !s.segExited && s.segRunID == runID
		if ok {
			s.live = true
		}
		cb := s.opts.OnSegmentStart
		s.mu.Unlock()
		if ok && cb != nil {
			cb()
		}
	}()

	err := s.cmd.Wait()
	s.stopCSVWatcher()

	s.mu.Lock()
	s.segExited = true
	s.mu.Unlock()

	s.mu.Lock()
	wasRestart := s.restartRequested
	s.restartRequested = false
	s.mu.Unlock()

	if err != nil && s.ctx.Err() != context.Canceled {

		if wasRestart || isInterrupted(err) {
			s.processCSVNewLines(indexFile)
			return nil
		}
		return fmt.Errorf("ffmpeg exited abnormally: %w (stderr tail: %s)", err, s.stderrTailString())
	}

	s.processCSVNewLines(indexFile)

	return nil
}

func (s *Stream) startCSVWatcher(indexFile string) {
	if s.csvWatcher != nil {
		return
	}
	s.csvWatcher = make(chan struct{})
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-s.csvWatcher:
				return
			case <-s.ctx.Done():
				return
			case <-ticker.C:
				s.processCSVNewLines(indexFile)
			}
		}
	}()
}

func (s *Stream) stopCSVWatcher() {
	if s.csvWatcher != nil {
		close(s.csvWatcher)
		s.csvWatcher = nil
	}
}

func (s *Stream) processCSVNewLines(indexFile string) {
	data, err := os.ReadFile(indexFile)
	if err != nil {
		return
	}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Split(line, ",")
		if len(parts) < 2 {
			continue
		}
		f := parts[0]
		if !strings.HasSuffix(f, ".mp4") {
			continue
		}

		s.processedMu.Lock()
		if s.processedSegs[f] {
			s.processedMu.Unlock()
			continue
		}
		s.processedSegs[f] = true
		idx := s.segmentIdx
		s.segmentIdx++
		s.processedMu.Unlock()

		full := filepath.Join(s.opts.OutputDir, f)
		info, err := os.Stat(full)
		if err != nil || info.Size() == 0 {

			s.processedMu.Lock()
			delete(s.processedSegs, f)
			s.segmentIdx--
			s.processedMu.Unlock()
			continue
		}

		segStart := s.runStartTime
		segEnd := time.Now()
		if len(parts) >= 3 {
			if so, err := strconv.ParseFloat(parts[1], 64); err == nil {
				segStart = s.runStartTime.Add(time.Duration(so * float64(time.Second)))
			}
			if eo, err := strconv.ParseFloat(parts[2], 64); err == nil {
				segEnd = s.runStartTime.Add(time.Duration(eo * float64(time.Second)))
			}
		}
		segInfo := &SegmentInfo{
			Index:     idx,
			StartTime: segStart,
			EndTime:   segEnd,
			FilePath:  full,
			FileSize:  info.Size(),
			IndexPath: indexFile,
			Duration:  segEnd.Sub(segStart).Seconds(),
		}
		if s.opts.OnSegment != nil {
			s.opts.OnSegment(s.opts.CameraID, segInfo)
		}
	}
}

func isInterrupted(err error) bool {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if ws, ok := ee.ProcessState.Sys().(syscall.WaitStatus); ok {
			return ws.Signaled() && ws.Signal() == syscall.SIGINT
		}
	}
	return false
}

func (s *Stream) getRTSPURL() string {
	return s.rtspURL
}

func (s *Stream) readOutput(pipe interface{}, prefix string) {
	rc, ok := pipe.(io.ReadCloser)
	if !ok {
		return
	}
	defer rc.Close()
	buf := make([]byte, 4096)
	for {
		n, err := rc.Read(buf)
		if n > 0 && prefix == "stderr" {

			s.stderrMu.Lock()
			s.stderrTail.Write(buf[:n])
			if s.stderrTail.Len() > 16384 {
				b := make([]byte, s.stderrTail.Len())
				copy(b, s.stderrTail.Bytes())
				s.stderrTail.Reset()
				s.stderrTail.Write(b[len(b)-8192:])
			}
			s.stderrMu.Unlock()
		}
		if err != nil {
			return
		}
	}
}

func (s *Stream) stderrTailString() string {
	s.stderrMu.Lock()
	defer s.stderrMu.Unlock()
	return strings.TrimSpace(s.stderrTail.String())
}

func (s *Stream) Stop() error {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return nil
	}
	var proc *os.Process
	if s.cmd != nil {
		proc = s.cmd.Process
	}
	s.live = false
	s.mu.Unlock()

	s.cancel()
	if proc != nil {

		proc.Signal(os.Interrupt)
	}

	select {
	case <-s.doneChan:
	case <-time.After(5 * time.Second):
		if proc != nil {
			proc.Kill()
		}

		select {
		case <-s.doneChan:
		case <-time.After(10 * time.Second):
			logrus.Warnf("stream %d did not exit within timeout", s.opts.CameraID)
		}
	}
	return nil
}

func (s *Stream) Restart() {
	s.mu.Lock()
	var proc *os.Process
	if s.cmd != nil {
		proc = s.cmd.Process
	}
	running := s.running
	if running {
		s.restartRequested = true
	}
	s.mu.Unlock()

	if !running || proc == nil {
		return
	}
	proc.Signal(os.Interrupt)
}

func (s *Stream) IsRunning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}

// IsLive 报告当前片段是否已确认正常运行（真正连上流源）。
// 流源不可达时（自动重试中）返回 false，区别于 IsRunning（重试管线存活即 true）。
func (s *Stream) IsLive() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running && s.live
}

func (s *Stream) IsHealthy() bool {
	s.mu.Lock()
	running := s.running
	outDir := s.opts.OutputDir
	startTime := s.startTime
	recordOnly := s.opts.RecordOnly
	s.mu.Unlock()
	if !running {
		return false
	}

	gracePeriod := 30 * time.Second

	if recordOnly {

		newest := s.newestSegmentModTime(outDir)
		if newest.IsZero() {
			return time.Since(startTime) < gracePeriod
		}
		return time.Since(newest) < 90*time.Second
	}

	info, err := os.Stat(filepath.Join(outDir, "index.m3u8"))
	if err != nil {

		return time.Since(startTime) < gracePeriod
	}
	return time.Since(info.ModTime()) < 60*time.Second
}

func (s *Stream) newestSegmentModTime(outDir string) time.Time {
	entries, err := os.ReadDir(outDir)
	if err != nil {
		return time.Time{}
	}
	var newest time.Time
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "segment_") || !strings.HasSuffix(e.Name(), ".mp4") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().After(newest) {
			newest = info.ModTime()
		}
	}
	return newest
}

func (s *Stream) Done() <-chan struct{} {
	return s.doneChan
}

func (s *Stream) Snapshot() (string, error) {
	s.mu.Lock()
	running := s.running
	rtspURL := s.rtspURL
	outDir := s.opts.OutputDir
	cameraID := s.opts.CameraID
	recordOnly := s.opts.RecordOnly
	s.mu.Unlock()

	if !running {
		return "", fmt.Errorf("stream not running")
	}

	snapshotPath := filepath.Join(outDir, fmt.Sprintf("snapshot_%d_%d.jpg", cameraID, time.Now().Unix()))

	if recordOnly {

		args := []string{
			"-y",
			"-rtsp_transport", "tcp",
			"-i", rtspURL,
			"-vframes", "1",
			"-q:v", "2",
			snapshotPath,
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "ffmpeg", args...)
		if err := cmd.Run(); err != nil {
			return "", fmt.Errorf("snapshot failed: %w", err)
		}
		return snapshotPath, nil
	}

	hlsPlaylist := filepath.Join(outDir, "index.m3u8")
	if _, err := os.Stat(hlsPlaylist); err != nil {
		return "", fmt.Errorf("HLS playlist does not exist, preview stream is not ready yet")
	}

	args := []string{
		"-y",
		"-i", hlsPlaylist,
		"-vframes", "1",
		"-q:v", "2",
		snapshotPath,
	}

	cmd := exec.CommandContext(context.Background(), "ffmpeg", args...)
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("snapshot failed: %w", err)
	}

	return snapshotPath, nil
}

func (m *Manager) GetStream(cameraID uint) (*Stream, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.streams[cameraID]
	return s, ok
}

func (m *Manager) StopAll() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, s := range m.streams {
		s.Stop()
	}
	m.streams = make(map[uint]*Stream)
}

func ProbeStream(rtspURL string) (*StreamInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "ffprobe",
		"-v", "quiet",
		"-print_format", "json",
		"-show_streams",
		"-show_format",
		"-rtsp_transport", "tcp",
		rtspURL,
	)

	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ffprobe failed: %w", err)
	}

	info := &StreamInfo{}

	_ = output

	return info, nil
}

type StreamInfo struct {
	Width    int
	Height   int
	Codec    string
	FPS      float64
	Bitrate  int64
	Duration float64
}

type HLSTranscoder struct {
	cameraID  uint
	rtspURL   string
	outputDir string
	cmd       *exec.Cmd
	ctx       context.Context
	cancel    context.CancelFunc
}

func NewHLSTranscoder(cameraID uint, rtspURL, outputDir string) *HLSTranscoder {
	ctx, cancel := context.WithCancel(context.Background())
	return &HLSTranscoder{
		cameraID:  cameraID,
		rtspURL:   rtspURL,
		outputDir: outputDir,
		ctx:       ctx,
		cancel:    cancel,
	}
}

func (h *HLSTranscoder) Start() error {
	if err := os.MkdirAll(h.outputDir, 0755); err != nil {
		return err
	}

	args := []string{
		"-y",
		"-rtsp_transport", "tcp",
		"-i", h.rtspURL,
		"-c:v", "libx264",
		"-preset", "veryfast",
		"-tune", "zerolatency",
		"-c:a", "aac",
		"-b:a", "128k",
		"-f", "hls",
		"-hls_time", "2",
		"-hls_list_size", "6",
		"-hls_flags", "delete_segments+append_list",
		"-hls_segment_filename", filepath.Join(h.outputDir, "segment_%03d.ts"),
		filepath.Join(h.outputDir, "index.m3u8"),
	}

	h.cmd = exec.CommandContext(h.ctx, "ffmpeg", args...)
	return h.cmd.Start()
}

func (h *HLSTranscoder) Stop() error {
	h.cancel()
	if h.cmd != nil && h.cmd.Process != nil {
		return h.cmd.Process.Kill()
	}
	return nil
}

type PreviewStream struct {
	cameraID  uint
	rtspURL   string
	outputDir string
	cmd       *exec.Cmd
	ctx       context.Context
	cancel    context.CancelFunc
	doneChan  chan struct{}
	running   bool
	startTime time.Time
	mu        sync.Mutex

	Src string
}

func NewPreviewStream(cameraID uint, rtspURL, outputDir string) *PreviewStream {
	ctx, cancel := context.WithCancel(context.Background())
	return &PreviewStream{
		cameraID:  cameraID,
		rtspURL:   rtspURL,
		outputDir: outputDir,
		ctx:       ctx,
		cancel:    cancel,
		doneChan:  make(chan struct{}),
	}
}

func (p *PreviewStream) Start() error {
	p.mu.Lock()
	if p.running {
		p.mu.Unlock()
		return nil
	}
	if err := os.MkdirAll(p.outputDir, 0755); err != nil {
		p.mu.Unlock()
		return err
	}

	startSeq := p.nextStartSequence()
	p.removeHLSFiles(p.listHLSFiles())

	args := []string{
		"-y",
		"-rtsp_transport", "tcp",

		"-i", p.rtspURL,
		"-map", "0:v:0",
		"-c:v", "libx264",
		"-preset", "veryfast",
		"-tune", "zerolatency",
		"-g", "50",
		"-keyint_min", "50",
		"-sc_threshold", "0",
		"-pix_fmt", "yuv420p",
		"-an",
		"-f", "hls",
		"-hls_time", "2",
		"-hls_list_size", "6",
		"-hls_flags", "delete_segments+append_list",
		"-hls_segment_filename", filepath.Join(p.outputDir, "hls_segment_%03d.ts"),
	}

	if startSeq > 0 {
		args = append(args, "-start_number", strconv.Itoa(startSeq))
	}
	args = append(args, filepath.Join(p.outputDir, "index.m3u8"))

	p.cmd = exec.CommandContext(p.ctx, "ffmpeg", args...)
	if err := p.cmd.Start(); err != nil {
		p.mu.Unlock()
		return err
	}
	p.running = true
	p.startTime = time.Now()
	p.mu.Unlock()

	go func() {
		p.cmd.Wait()
		p.mu.Lock()
		p.running = false
		p.mu.Unlock()
		close(p.doneChan)
	}()
	return nil
}

func (p *PreviewStream) Stop() {
	p.mu.Lock()
	running := p.running
	var proc *os.Process
	if p.cmd != nil {
		proc = p.cmd.Process
	}
	p.mu.Unlock()

	if !running {
		return
	}

	stale := p.listHLSFiles()
	p.cancel()
	if proc != nil {
		proc.Kill()
	}
	select {
	case <-p.doneChan:
	case <-time.After(5 * time.Second):
	}

	p.removeHLSFiles(stale)

	if maxSeq := maxHLSSequenceFromNames(stale); maxSeq >= 0 {
		os.WriteFile(filepath.Join(p.outputDir, hlsSeqStateFile), []byte(strconv.Itoa(maxSeq)), 0644)
	}
}

func (p *PreviewStream) IsRunning() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.running
}

func (p *PreviewStream) listHLSFiles() []string {
	entries, err := os.ReadDir(p.outputDir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if name == "index.m3u8" || (strings.HasPrefix(name, "hls_segment_") && strings.HasSuffix(name, ".ts")) {
			names = append(names, name)
		}
	}
	return names
}

const hlsSeqStateFile = ".hls_last_seq"

func (p *PreviewStream) nextStartSequence() int {
	last := -1
	if data, err := os.ReadFile(filepath.Join(p.outputDir, hlsSeqStateFile)); err == nil {
		if n, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil && n > last {
			last = n
		}
	}
	if disk := maxHLSSequenceFromNames(p.listHLSFiles()); disk > last {
		last = disk
	}
	return last + 1
}

func maxHLSSequenceFromNames(names []string) int {
	max := -1
	for _, name := range names {
		if !strings.HasPrefix(name, "hls_segment_") || !strings.HasSuffix(name, ".ts") {
			continue
		}
		num := 0
		if _, err := fmt.Sscanf(name[len("hls_segment_"):len(name)-3], "%d", &num); err == nil && num > max {
			max = num
		}
	}
	return max
}
func (p *PreviewStream) removeHLSFiles(names []string) {
	for _, name := range names {
		if os.Remove(filepath.Join(p.outputDir, name)) == nil {
			logrus.Debugf("preview stream camera=%d removing leftover HLS file: %s", p.cameraID, name)
		}
	}
}

func (p *PreviewStream) Done() <-chan struct{} {
	return p.doneChan
}

func (p *PreviewStream) IsHealthy() bool {
	p.mu.Lock()
	running := p.running
	outDir := p.outputDir
	startTime := p.startTime
	p.mu.Unlock()
	if !running {
		return false
	}
	info, err := os.Stat(filepath.Join(outDir, "index.m3u8"))
	if err != nil {
		return time.Since(startTime) < 15*time.Second
	}
	return time.Since(info.ModTime()) < 45*time.Second
}
