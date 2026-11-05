package storage

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"platfarm/svc-vision/internal/config"
)

type fakeWebdavServer struct {
	mu      sync.Mutex
	files   map[string]int64
	mtimes  map[string]time.Time
	deleted []string
}

func (f *fakeWebdavServer) record(path string, size int64, mod time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.files["/"+strings.TrimPrefix(path, "/")] = size
	f.mtimes["/"+strings.TrimPrefix(path, "/")] = mod
}

func (f *fakeWebdavServer) handler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "PROPFIND":
		f.mu.Lock()
		defer f.mu.Unlock()
		var sb strings.Builder
		sb.WriteString(`<?xml version="1.0" encoding="utf-8"?><multistatus xmlns="DAV:">`)

		sb.WriteString(`<response><href>` + r.URL.Path + `</href><propstat><prop><resourcetype><collection/></resourcetype></prop></propstat></response>`)
		for p, sz := range f.files {
			if !strings.HasPrefix(p, r.URL.Path) {
				continue
			}
			t := f.mtimes[p].Format(time.RFC1123)
			sb.WriteString(`<response><href>` + "http://h" + p + `</href><propstat><prop>` +
				`<resourcetype/>` +
				`<getcontentlength>` + fmt.Sprint(sz) + `</getcontentlength>` +
				`<getlastmodified>` + t + `</getlastmodified>` +
				`</prop></propstat></response>`)
		}
		sb.WriteString(`</multistatus>`)
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusMultiStatus)
		w.Write([]byte(sb.String()))
	case "DELETE":
		f.mu.Lock()
		defer f.mu.Unlock()
		p := "/" + strings.TrimPrefix(r.URL.Path, "/")
		delete(f.files, p)
		delete(f.mtimes, p)
		f.deleted = append(f.deleted, p)
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func TestWebdavCleanupRetention(t *testing.T) {
	f := &fakeWebdavServer{files: map[string]int64{}, mtimes: map[string]time.Time{}}
	ts := httptest.NewServer(http.HandlerFunc(f.handler))
	defer ts.Close()

	now := time.Now()
	f.record("/surveillance/old1.mp4", 1000, now.AddDate(0, 0, -40))
	f.record("/surveillance/old2.mp4", 2000, now.AddDate(0, 0, -35))
	f.record("/surveillance/recent1.mp4", 3000, now.AddDate(0, 0, -5))

	m := &Manager{}
	m.runtime = &RuntimeStorage{webdav: config.WebdavConfig{
		Enabled: true, URL: ts.URL, BasePath: "surveillance", MaxDays: 30,
	}}

	m.doCleanupWebdav()

	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.files["/surveillance/old1.mp4"]; ok {
		t.Errorf("old1.mp4（-40天）应被按保留天数删除")
	}
	if _, ok := f.files["/surveillance/old2.mp4"]; ok {
		t.Errorf("old2.mp4（-35天）应被按保留天数删除")
	}
	if _, ok := f.files["/surveillance/recent1.mp4"]; !ok {
		t.Errorf("recent1.mp4（-5天）不应被删除")
	}
}

func TestWebdavCleanupDisabled(t *testing.T) {
	m := &Manager{}
	m.runtime = &RuntimeStorage{webdav: config.WebdavConfig{Enabled: false}}
	m.doCleanupWebdav()
}

func TestWebdavCleanupCapacity(t *testing.T) {
	f := &fakeWebdavServer{files: map[string]int64{}, mtimes: map[string]time.Time{}}
	ts := httptest.NewServer(http.HandlerFunc(f.handler))
	defer ts.Close()

	now := time.Now()

	f.record("/surveillance/a.mp4", 1000, now.AddDate(0, 0, -3))
	f.record("/surveillance/b.mp4", 1000, now.AddDate(0, 0, -2))
	f.record("/surveillance/c.mp4", 1000, now.AddDate(0, 0, -1))

	m := &Manager{}

	m.runtime = &RuntimeStorage{webdav: config.WebdavConfig{
		Enabled: true, URL: ts.URL, BasePath: "surveillance", MaxDays: 0, MaxStorageGB: 1500.0 / 1073741824.0,
	}}
	m.doCleanupWebdav()

	f.mu.Lock()
	defer f.mu.Unlock()

	if _, ok := f.files["/surveillance/a.mp4"]; ok {
		t.Errorf("a.mp4（最旧）应被容量清理删除")
	}
	if _, ok := f.files["/surveillance/b.mp4"]; ok {
		t.Errorf("b.mp4 应被容量清理删除")
	}
	if _, ok := f.files["/surveillance/c.mp4"]; !ok {
		t.Errorf("c.mp4（最新）不应被删除")
	}
}
