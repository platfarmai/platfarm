package onvif

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

type Client struct {
	timeout    time.Duration
	httpClient *http.Client
	username   string
	password   string

	digestAuth *digestAuthState
}

type digestAuthState struct {
	realm     string
	nonce     string
	qop       string
	algorithm string
	opaque    string
	nc        int
}

type DeviceInfo struct {
	IP           string    `json:"ip"`
	Port         int       `json:"port"`
	Name         string    `json:"name"`
	Manufacturer string    `json:"manufacturer"`
	Model        string    `json:"model"`
	Firmware     string    `json:"firmware"`
	SerialNumber string    `json:"serialNumber"`
	HardwareId   string    `json:"hardwareId"`
	MAC          string    `json:"mac"`
	Profiles     []Profile `json:"profiles"`
	XAddr        string    `json:"xaddr"`
	AuthRequired bool      `json:"auth_required"`
}

type Profile struct {
	Token                 string `json:"token"`
	Name                  string `json:"name"`
	Width                 int    `json:"width"`
	Height                int    `json:"height"`
	FPS                   int    `json:"fps"`
	Bitrate               int    `json:"bitrate"`
	Codec                 string `json:"codec"`
	RTSPUri               string `json:"rtspUri"`
	VideoSourceTok        string `json:"videoSourceTok"`
	VideoEncoderTok       string `json:"videoEncoderTok"`
	PTZConfigurationToken string `json:"ptzConfigurationToken"`
}

type StreamTransport string

const (
	TransportUDP  StreamTransport = "UDP"
	TransportTCP  StreamTransport = "TCP"
	TransportHTTP StreamTransport = "HTTP"
	TransportRTSP StreamTransport = "RTSP"
)

type StreamProfile struct {
	ProfileToken string
	Transport    StreamTransport
	StreamType   string
}

func NewClient(timeoutSec int) *Client {
	return &Client{
		timeout: time.Duration(timeoutSec) * time.Second,
		httpClient: &http.Client{
			Timeout: time.Duration(timeoutSec) * time.Second,
		},
	}
}

func (c *Client) SetCredentials(username, password string) {
	c.username = username
	c.password = password
	c.digestAuth = nil
}

func (c *Client) Discover(network string) ([]*DeviceInfo, error) {
	devices, err := c.wsDiscovery(network)
	if err != nil {
		return nil, err
	}

	var results []*DeviceInfo
	for _, dev := range devices {
		info, err := c.GetDeviceInfo(dev.XAddr)
		if err != nil {
			continue
		}
		results = append(results, info)
	}

	return results, nil
}

func (c *Client) wsDiscovery(network string) ([]*DeviceInfo, error) {
	var devices []*DeviceInfo

	ip, ipNet, err := net.ParseCIDR(network)
	if err != nil {
		if dev, _ := c.probeSingle(network); dev != nil {
			devices = append(devices, dev)
		}
		return devices, nil
	}

	for ip := ip.Mask(ipNet.Mask); ipNet.Contains(ip); incIP(ip) {
		if ip.Equal(ipNet.IP) || ip.Equal(broadcastIP(ipNet)) {
			continue
		}
		if dev, _ := c.probeSingle(ip.String()); dev != nil {
			devices = append(devices, dev)
		}
	}

	return devices, nil
}

func (c *Client) ProbeSingle(ip string) *DeviceInfo {
	info, _ := c.probeSingle(ip)
	return info
}

func (c *Client) ProbeSingleEx(ip string) (*DeviceInfo, bool) {
	return c.probeSingle(ip)
}

func (c *Client) probeSingle(ip string) (*DeviceInfo, bool) {
	ports := []int{80, 8000, 8080, 5000, 8899}

	type result struct {
		port         int
		info         *DeviceInfo
		authRequired bool
	}

	resultChan := make(chan result, len(ports))
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()

	for _, port := range ports {
		go func(p int) {
			addr := fmt.Sprintf("http://%s:%d/onvif/device_service", ip, p)
			select {
			case <-ctx.Done():
				resultChan <- result{port: p}
			default:
				info, authRequired := c.getDeviceInfo(addr)
				if info != nil {

					if c.username != "" && c.password != "" {
						mediaAddr := c.ResolveMediaXAddr(addr)
						profiles, err := c.GetProfiles(mediaAddr)
						if (err != nil || len(profiles) == 0) && mediaAddr != addr {

							profiles, err = c.GetProfiles(addr)
						}
						if err == nil && len(profiles) > 0 {
							info.Profiles = profiles
						}
					}
					resultChan <- result{port: p, info: info}
				} else {
					resultChan <- result{port: p, authRequired: authRequired}
				}
			}
		}(port)
	}

	sawAuthRequired := false
	completed := 0
	for completed < len(ports) {
		select {
		case r := <-resultChan:
			completed++
			if r.info != nil {
				cancel()
				return r.info, false
			}
			if r.authRequired {
				sawAuthRequired = true
			}
		case <-ctx.Done():

			for completed < len(ports) {
				select {
				case r := <-resultChan:
					if r.info != nil {
						return r.info, false
					}
					if r.authRequired {
						sawAuthRequired = true
					}
					completed++
				default:
					goto done
				}
			}
		}
	}
done:
	return nil, sawAuthRequired
}

func (c *Client) doRequest(ctx context.Context, xaddr, soapAction, body string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", xaddr, bytes.NewReader([]byte(body)))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/soap+xml; charset=utf-8")
	req.Header.Set("SOAPAction", soapAction)

	if c.username != "" && c.password != "" {
		if c.digestAuth != nil && c.digestAuth.nonce != "" {

			authHeader := c.buildDigestAuth(req.Method, xaddr)
			req.Header.Set("Authorization", authHeader)
		} else {

			req.SetBasicAuth(c.username, c.password)
		}
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode == 401 && c.username != "" && c.password != "" {
		resp.Body.Close()
		return c.doRequestWithDigest(ctx, xaddr, soapAction, body, resp.Header.Get("WWW-Authenticate"))
	}

	return resp, nil
}

func (c *Client) doRequestWithDigest(ctx context.Context, xaddr, soapAction, body, wwwAuth string) (*http.Response, error) {

	c.parseDigestChallenge(wwwAuth)

	req, err := http.NewRequestWithContext(ctx, "POST", xaddr, bytes.NewReader([]byte(body)))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/soap+xml; charset=utf-8")
	req.Header.Set("SOAPAction", soapAction)

	authHeader := c.buildDigestAuth(req.Method, xaddr)
	req.Header.Set("Authorization", authHeader)

	return c.httpClient.Do(req)
}

func (c *Client) parseDigestChallenge(challenge string) {

	c.digestAuth = &digestAuthState{}

	challenge = strings.TrimPrefix(challenge, "Digest ")

	parts := strings.Split(challenge, ",")
	for _, part := range parts {
		part = strings.TrimSpace(part)

		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 {
			continue
		}
		key := strings.TrimSpace(kv[0])
		val := strings.Trim(kv[1], "\"")

		switch key {
		case "realm":
			c.digestAuth.realm = val
		case "nonce":
			c.digestAuth.nonce = val
		case "qop":
			c.digestAuth.qop = val
		case "algorithm":
			c.digestAuth.algorithm = val
		case "opaque":
			c.digestAuth.opaque = val
		}
	}

	if c.digestAuth.algorithm == "" {
		c.digestAuth.algorithm = "MD5"
	}
}

func (c *Client) buildDigestAuth(method, uri string) string {
	if c.digestAuth == nil || c.digestAuth.nonce == "" {
		return ""
	}

	c.digestAuth.nc++
	nc := fmt.Sprintf("%08x", c.digestAuth.nc)
	cnonce := generateCnonce()

	ha1 := md5.Sum([]byte(fmt.Sprintf("%s:%s:%s", c.username, c.digestAuth.realm, c.password)))
	ha1Str := hex.EncodeToString(ha1[:])

	ha2 := md5.Sum([]byte(fmt.Sprintf("%s:%s", method, uri)))
	ha2Str := hex.EncodeToString(ha2[:])

	response := md5.Sum([]byte(fmt.Sprintf("%s:%s:%s:%s:%s:%s",
		ha1Str, c.digestAuth.nonce, nc, cnonce, c.digestAuth.qop, ha2Str)))
	responseStr := hex.EncodeToString(response[:])

	auth := fmt.Sprintf(`Digest username="%s", realm="%s", nonce="%s", uri="%s", algorithm=%s, qop=%s, nc=%s, cnonce="%s", response="%s"`,
		c.username, c.digestAuth.realm, c.digestAuth.nonce, uri, c.digestAuth.algorithm, c.digestAuth.qop, nc, cnonce, responseStr)

	if c.digestAuth.opaque != "" {
		auth += fmt.Sprintf(`, opaque="%s"`, c.digestAuth.opaque)
	}

	return auth
}

func generateCnonce() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {

		return fmt.Sprintf("%x", time.Now().UnixNano())[:16]
	}
	return hex.EncodeToString(b)[:16]
}

type Capabilities struct {
	DeviceXAddr  string
	MediaXAddr   string
	EventsXAddr  string
	PTZXAddr     string
	ImagingXAddr string
}

func (c *Client) GetCapabilities(xaddr string) (*Capabilities, error) {
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()

	resp, err := c.doRequest(ctx, xaddr,
		`"http://www.onvif.org/ver10/device/wsdl/GetCapabilities"`,
		getCapabilitiesBody())
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	raw := string(body)

	caps := &Capabilities{
		DeviceXAddr:  extractServiceXAddr(raw, "Device"),
		MediaXAddr:   extractServiceXAddr(raw, "Media"),
		EventsXAddr:  extractServiceXAddr(raw, "Events"),
		PTZXAddr:     extractServiceXAddr(raw, "PTZ"),
		ImagingXAddr: extractServiceXAddr(raw, "Imaging"),
	}

	if caps.DeviceXAddr == "" {
		caps.DeviceXAddr = xaddr
	}
	if caps.MediaXAddr == "" {
		caps.MediaXAddr = xaddr
	}
	if caps.EventsXAddr == "" {
		caps.EventsXAddr = xaddr
	}
	return caps, nil
}

func (c *Client) ResolveMediaXAddr(deviceXAddr string) string {
	caps, err := c.GetCapabilities(deviceXAddr)
	if err == nil && caps.MediaXAddr != "" {
		return caps.MediaXAddr
	}
	return deviceXAddr
}

func (c *Client) ResolveEventsXAddr(deviceXAddr string) string {
	caps, err := c.GetCapabilities(deviceXAddr)
	if err == nil && caps.EventsXAddr != "" {
		return caps.EventsXAddr
	}
	return deviceXAddr
}

func extractServiceXAddr(raw, service string) string {
	re := regexp.MustCompile(`(?s)<(?:[^>]*:)?` + service + `[^>]*>\s*<(?:[^>]*:)?XAddr[^>]*>([^<]+)</(?:[^>]*:)?XAddr>`)
	if m := re.FindStringSubmatch(raw); len(m) >= 2 {
		return strings.TrimSpace(m[1])
	}
	return ""
}

func (c *Client) getDeviceInfo(xaddr string) (*DeviceInfo, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()

	resp, err := c.doRequest(ctx, xaddr,
		`"http://www.onvif.org/ver10/device/wsdl/GetDeviceInformation"`,
		getDeviceInfoBody())
	if err != nil {
		return nil, false
	}
	defer resp.Body.Close()

	if resp.StatusCode == 401 {
		return nil, true
	}

	body, _ := io.ReadAll(resp.Body)
	return parseDeviceInfo(xaddr, string(body)), false
}

func (c *Client) GetDeviceInfo(xaddr string) (*DeviceInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()

	resp, err := c.doRequest(ctx, xaddr,
		`"http://www.onvif.org/ver10/device/wsdl/GetDeviceInformation"`,
		getDeviceInfoBody())
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	info := parseDeviceInfo(xaddr, string(body))
	if info == nil {
		return nil, fmt.Errorf("failed to parse device information")
	}
	return info, nil
}

func (c *Client) GetProfiles(xaddr string) ([]Profile, error) {
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()

	resp, err := c.doRequest(ctx, xaddr,
		`"http://www.onvif.org/ver10/media/wsdl/GetProfiles"`,
		getProfilesBody())
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	return parseProfiles(string(body))
}

func (c *Client) GetStreamUri(xaddr, profileToken string, transport StreamTransport) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()

	body := fmt.Sprintf(getStreamUriBody(transport), profileToken)
	resp, err := c.doRequest(ctx, xaddr,
		`"http://www.onvif.org/ver10/media/wsdl/GetStreamUri"`,
		body)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	return parseStreamUri(string(respBody))
}

func (c *Client) GetStreamUriWithRetry(xaddr string, profiles []Profile, preferredToken string, transport StreamTransport, maxRetries int) (string, *Profile, error) {

	order := make([]Profile, 0, len(profiles))
	if preferredToken != "" {
		for _, p := range profiles {
			if p.Token == preferredToken {
				order = append(order, p)
			}
		}
	}
	for _, p := range profiles {
		if p.Token != preferredToken {
			order = append(order, p)
		}
	}

	var lastErr error

	for i := 0; i < maxRetries && i < len(order); i++ {
		profile := order[i]
		uri, err := c.GetStreamUri(xaddr, profile.Token, transport)
		if err == nil && uri != "" {
			return uri, &profile, nil
		}
		lastErr = err

	}

	return "", nil, fmt.Errorf("all profile attempts failed: %v", lastErr)
}

func (c *Client) PTZControl(xaddr, command string, speed float64) error {
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()

	body := getPTZBody(command, speed)
	resp, err := c.doRequest(ctx, xaddr,
		`"http://www.onvif.org/ver20/ptz/wsdl/ContinuousMove"`,
		body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return nil
}

func getDeviceInfoBody() string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope">
  <s:Body xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xmlns:xsd="http://www.w3.org/2001/XMLSchema">
    <GetDeviceInformation xmlns="http://www.onvif.org/ver10/device/wsdl"/>
  </s:Body>
</s:Envelope>`
}

func getProfilesBody() string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope">
  <s:Body xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xmlns:xsd="http://www.w3.org/2001/XMLSchema">
    <GetProfiles xmlns="http://www.onvif.org/ver10/media/wsdl"/>
  </s:Body>
</s:Envelope>`
}

func getCapabilitiesBody() string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope">
  <s:Body xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xmlns:xsd="http://www.w3.org/2001/XMLSchema">
    <GetCapabilities xmlns="http://www.onvif.org/ver10/device/wsdl">
      <Category>All</Category>
    </GetCapabilities>
  </s:Body>
</s:Envelope>`
}

func getStreamUriBody(transport StreamTransport) string {
	proto := "RTSP"
	switch transport {
	case TransportUDP:
		proto = "RTP-Unicast"
	case TransportTCP:
		proto = "RTP-Unicast"
	case TransportHTTP:
		proto = "RTP-Unicast"
	}

	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope">
  <s:Body xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xmlns:xsd="http://www.w3.org/2001/XMLSchema">
    <GetStreamUri xmlns="http://www.onvif.org/ver10/media/wsdl">
      <StreamSetup>
        <Stream xmlns="http://www.onvif.org/ver10/schema">%s</Stream>
        <Transport xmlns="http://www.onvif.org/ver10/schema">
          <Protocol>%s</Protocol>
        </Transport>
      </StreamSetup>
      <ProfileToken>%%s</ProfileToken>
    </GetStreamUri>
  </s:Body>
</s:Envelope>`, proto, strings.ToUpper(string(transport)))
}

func getPTZBody(command string, speed float64) string {
	var x, y, z float64
	switch command {
	case "up":
		y = speed
	case "down":
		y = -speed
	case "left":
		x = -speed
	case "right":
		x = speed
	case "zoom_in":
		z = speed
	case "zoom_out":
		z = -speed
	case "stop":
		x, y, z = 0, 0, 0
	}

	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope">
  <s:Body xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xmlns:xsd="http://www.w3.org/2001/XMLSchema">
    <ContinuousMove xmlns="http://www.onvif.org/ver20/ptz/wsdl">
      <ProfileToken>%s</ProfileToken>
      <Velocity>
        <PanTilt x="%f" y="%f" xmlns="http://www.onvif.org/ver10/schema"/>
        <Zoom x="%f" xmlns="http://www.onvif.org/ver10/schema"/>
      </Velocity>
    </ContinuousMove>
  </s:Body>
</s:Envelope>`, "profile_token_placeholder", x, y, z)
}

type soapEnvelope struct {
	XMLName xml.Name `xml:"Envelope"`
	Body    soapBody `xml:"Body"`
}

type soapBody struct {
	XMLName    xml.Name       `xml:"Body"`
	DeviceInfo *devInfoResp   `xml:"GetDeviceInformationResponse"`
	Profiles   *profilesResp  `xml:"GetProfilesResponse"`
	StreamUri  *streamUriResp `xml:"GetStreamUriResponse"`
	Fault      *soapFault     `xml:"Fault"`
}

type devInfoResp struct {
	Manufacturer string `xml:"Manufacturer"`
	Model        string `xml:"Model"`
	Firmware     string `xml:"FirmwareVersion"`
	SerialNumber string `xml:"SerialNumber"`
	HardwareId   string `xml:"HardwareId"`
}

type profilesResp struct {
	Profiles []profileXml `xml:"Profiles"`
}

type profileXml struct {
	Token                    string `xml:"token,attr"`
	Name                     string `xml:"Name"`
	VideoSourceConfiguration struct {
		Token string `xml:"token,attr"`
	} `xml:"VideoSourceConfiguration"`
	VideoEncoderConfiguration struct {
		Token      string `xml:"token,attr"`
		Name       string `xml:"Name"`
		Encoding   string `xml:"Encoding"`
		Resolution struct {
			Width  int `xml:"Width"`
			Height int `xml:"Height"`
		} `xml:"Resolution"`
		RateControl struct {
			BitrateLimit int `xml:"BitrateLimit"`
		} `xml:"RateControl"`
		Quality float64 `xml:"Quality"`
	} `xml:"VideoEncoderConfiguration"`
	PTZConfiguration struct {
		Token string `xml:"token,attr"`
	} `xml:"PTZConfiguration"`
}

type streamUriResp struct {
	MediaUri struct {
		Uri string `xml:"Uri"`
	} `xml:"MediaUri"`
}

type soapFault struct {
	Code   string `xml:"Code>Value"`
	String string `xml:"Reason>Text"`
}

func parseDeviceInfo(xaddr, body string) *DeviceInfo {
	var env soapEnvelope
	if err := xml.Unmarshal([]byte(body), &env); err != nil {
		return nil
	}

	if env.Body.Fault != nil {
		return nil
	}

	if env.Body.DeviceInfo == nil {
		return nil
	}

	info := &DeviceInfo{
		XAddr:        xaddr,
		Manufacturer: env.Body.DeviceInfo.Manufacturer,
		Model:        env.Body.DeviceInfo.Model,
		Firmware:     env.Body.DeviceInfo.Firmware,
		SerialNumber: env.Body.DeviceInfo.SerialNumber,
		HardwareId:   env.Body.DeviceInfo.HardwareId,
	}

	if strings.HasPrefix(xaddr, "http://") {
		hostPart := strings.TrimPrefix(xaddr, "http://")
		hostPart = strings.Split(hostPart, "/")[0]
		parts := strings.Split(hostPart, ":")
		info.IP = parts[0]
		if len(parts) > 1 {
			fmt.Sscanf(parts[1], "%d", &info.Port)
		} else {
			info.Port = 80
		}
	}

	return info
}

func parseProfiles(body string) ([]Profile, error) {
	var env soapEnvelope
	if err := xml.Unmarshal([]byte(body), &env); err != nil {
		return nil, err
	}

	if env.Body.Profiles == nil {
		return nil, nil
	}

	var profiles []Profile
	for _, p := range env.Body.Profiles.Profiles {
		profile := Profile{
			Token:                 p.Token,
			Name:                  p.Name,
			Width:                 p.VideoEncoderConfiguration.Resolution.Width,
			Height:                p.VideoEncoderConfiguration.Resolution.Height,
			Codec:                 strings.ToLower(p.VideoEncoderConfiguration.Encoding),
			Bitrate:               p.VideoEncoderConfiguration.RateControl.BitrateLimit,
			VideoSourceTok:        p.VideoSourceConfiguration.Token,
			VideoEncoderTok:       p.VideoEncoderConfiguration.Token,
			PTZConfigurationToken: p.PTZConfiguration.Token,
		}
		profiles = append(profiles, profile)
	}

	return profiles, nil
}

func parseStreamUri(body string) (string, error) {
	var env soapEnvelope
	if err := xml.Unmarshal([]byte(body), &env); err != nil {
		return "", err
	}

	if env.Body.StreamUri != nil {
		return env.Body.StreamUri.MediaUri.Uri, nil
	}

	return "", fmt.Errorf("stream URL not found")
}

func incIP(ip net.IP) {
	for i := len(ip) - 1; i >= 0; i-- {
		ip[i]++
		if ip[i] != 0 {
			break
		}
	}
}

func broadcastIP(ipNet *net.IPNet) net.IP {
	ip := make(net.IP, len(ipNet.IP))
	copy(ip, ipNet.IP)
	for i := range ip {
		ip[i] |= ^ipNet.Mask[i]
	}
	return ip
}

type probeMatch struct {
	XAddr        string
	Manufacturer string
	Model        string
	Firmware     string
	SerialNumber string
	HardwareId   string
	IP           string
	Port         int
}

func (c *Client) WSDiscover(timeoutSec int) ([]*DeviceInfo, error) {

	mcastAddr := &net.UDPAddr{
		IP:   net.ParseIP("239.255.255.250"),
		Port: 3702,
	}

	conn, err := net.ListenPacket("udp4", "0.0.0.0:0")
	if err != nil {
		return nil, fmt.Errorf("failed to listen on UDP: %w", err)
	}
	defer conn.Close()

	deadline := time.Now().Add(time.Duration(timeoutSec) * time.Second)
	conn.SetReadDeadline(deadline)

	messageID := fmt.Sprintf("uuid:%s", generateMessageID())
	probeMsg := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<e:Envelope xmlns:e="http://www.w3.org/2003/05/soap-envelope"
            xmlns:w="http://schemas.xmlsoap.org/ws/2005/04/discovery"
            xmlns:dn="http://www.onvif.org/ver10/network/wsdl">
  <e:Header>
    <w:MessageID>%s</w:MessageID>
    <w:To e:mustUnderstand="true">urn:schemas-xmlsoap-org:ws:2005:04:discovery</w:To>
    <w:Action e:mustUnderstand="true">http://schemas.xmlsoap.org/ws/2005/04/discovery/Probe</w:Action>
  </e:Header>
  <e:Body>
    <w:Probe>
      <w:Types>dn:NetworkVideoTransmitter</w:Types>
    </w:Probe>
  </e:Body>
</e:Envelope>`, messageID)

	if _, err := conn.WriteTo([]byte(probeMsg), mcastAddr); err != nil {
		return nil, fmt.Errorf("failed to send multicast Probe: %w", err)
	}

	matches := make(map[string]*probeMatch)
	buf := make([]byte, 8192)

	for {
		n, _, err := conn.ReadFrom(buf)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				break
			}
			continue
		}
		if n == 0 {
			continue
		}

		resp := string(buf[:n])

		if strings.Contains(resp, "ProbeMatches") {
			pm := c.parseProbeMatch(resp)
			if pm != nil {
				key := pm.XAddr
				if _, exists := matches[key]; !exists {
					matches[key] = pm
					logrus.Debugf("WS-Discovery device found: %s (%s)", pm.Manufacturer, pm.XAddr)
				}
			}
		}
	}

	var results []*DeviceInfo
	for _, pm := range matches {
		if pm.IP == "" || pm.Port == 0 {
			continue
		}
		dev := &DeviceInfo{
			IP:           pm.IP,
			Port:         pm.Port,
			XAddr:        pm.XAddr,
			Manufacturer: pm.Manufacturer,
			Model:        pm.Model,
			Firmware:     pm.Firmware,
			SerialNumber: pm.SerialNumber,
			HardwareId:   pm.HardwareId,
		}
		if dev.XAddr == "" {
			dev.XAddr = fmt.Sprintf("http://%s:%d/onvif/device_service", dev.IP, dev.Port)
		}

		if info, _ := c.getDeviceInfo(dev.XAddr); info != nil {
			dev.Name = info.Name
			dev.Manufacturer = firstNonEmpty(pm.Manufacturer, info.Manufacturer)
			dev.Model = firstNonEmpty(pm.Model, info.Model)
			dev.Firmware = firstNonEmpty(pm.Firmware, info.Firmware)
			dev.SerialNumber = firstNonEmpty(pm.SerialNumber, info.SerialNumber)
			dev.HardwareId = firstNonEmpty(pm.HardwareId, info.HardwareId)
		}
		results = append(results, dev)
	}

	return results, nil
}

var xaddrsRe = regexp.MustCompile(`<[^>]*XAddrs[^>]*>([^<]+)</[^>]*XAddrs>`)

func (c *Client) parseProbeMatch(body string) *probeMatch {

	pm := &probeMatch{}

	if m := xaddrsRe.FindStringSubmatch(body); len(m) > 1 {
		for _, x := range strings.Fields(m[1]) {
			if strings.HasPrefix(x, "http://") || strings.HasPrefix(x, "https://") {
				pm.XAddr = strings.TrimSpace(x)
				break
			}
		}
	}

	if pm.XAddr != "" {
		if strings.HasPrefix(pm.XAddr, "http://") {
			hostPart := strings.TrimPrefix(pm.XAddr, "http://")
			hostPart = strings.Split(hostPart, "/")[0]
			parts := strings.Split(hostPart, ":")
			pm.IP = parts[0]
			if len(parts) > 1 {
				fmt.Sscanf(parts[1], "%d", &pm.Port)
			} else {
				pm.Port = 80
			}
		}
	}

	extract := func(tag string) string {
		start := strings.Index(body, "<"+tag+">")
		if start < 0 {
			start = strings.Index(body, "<dn:"+tag+">")
		}
		if start < 0 {
			return ""
		}
		start += len(tag) + 2
		end := strings.Index(body[start:], "</"+tag+">")
		if end < 0 {
			end = strings.Index(body[start:], "</dn:"+tag+">")
		}
		if end < 0 {
			return ""
		}
		return strings.TrimSpace(body[start : start+end])
	}

	pm.Manufacturer = extract("Manufacturer")
	pm.Model = extract("Model")
	pm.Firmware = extract("FirmwareVersion")
	pm.SerialNumber = extract("SerialNumber")
	pm.HardwareId = extract("HardwareId")

	return pm
}

func generateMessageID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func (c *Client) quickONVIFCheck(ip string, timeout time.Duration) *DeviceInfo {
	client := &http.Client{Timeout: timeout}
	type found struct {
		dev *DeviceInfo
	}
	ch := make(chan *DeviceInfo, 3)

	for _, port := range []int{80, 8000, 8080} {
		go func(port int) {
			xaddr := fmt.Sprintf("http://%s:%d/onvif/device_service", ip, port)
			req, err := http.NewRequest("POST", xaddr, bytes.NewReader([]byte(getDeviceInfoBody())))
			if err != nil {
				return
			}
			req.Header.Set("Content-Type", "application/soap+xml; charset=utf-8")
			req.Header.Set("SOAPAction", `"http://www.onvif.org/ver10/device/wsdev/GetDeviceInformation"`)
			resp, err := client.Do(req)
			if err != nil {
				return
			}
			defer resp.Body.Close()

			switch {
			case resp.StatusCode == http.StatusUnauthorized:

				if resp.Header.Get("WWW-Authenticate") != "" {
					ch <- &DeviceInfo{IP: ip, Port: port, XAddr: xaddr, Name: ip, AuthRequired: true}
				}
			case resp.StatusCode == http.StatusOK:
				body, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
				info := parseDeviceInfo(xaddr, string(body))
				if info != nil {
					info.IP = ip
					info.Port = port
					info.XAddr = xaddr
					ch <- info
				}
			case resp.StatusCode == http.StatusMethodNotAllowed:

				ch <- &DeviceInfo{IP: ip, Port: port, XAddr: xaddr, Name: ip, AuthRequired: true}
			case resp.StatusCode == http.StatusBadRequest:
				body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
				if strings.Contains(string(body), "Fault") || strings.Contains(string(body), "onvif") {
					ch <- &DeviceInfo{IP: ip, Port: port, XAddr: xaddr, Name: ip, AuthRequired: true}
				}
			}
		}(port)
	}

	select {
	case dev := <-ch:
		return dev
	case <-time.After(timeout):
		return nil
	}
}

func (c *Client) SweepCIDR(cidr string, perProbe time.Duration) []*DeviceInfo {
	ip, ipNet, err := net.ParseCIDR(cidr)
	if err != nil {
		return nil
	}
	var ips []string
	for cur := ip.Mask(ipNet.Mask); ipNet.Contains(cur); {
		inc := cur
		if !inc.Equal(ipNet.IP) && !inc.Equal(broadcastIP(ipNet)) {
			ips = append(ips, cur.String())
		}
		next := make(net.IP, len(cur))
		copy(next, cur)
		for i := len(next) - 1; i >= 0; i-- {
			next[i]++
			if next[i] != 0 {
				break
			}
		}
		if !ipNet.Contains(next) {
			break
		}
		cur = next
	}

	results := make([]*DeviceInfo, 0)
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 128)

	for _, ipStr := range ips {
		wg.Add(1)
		sem <- struct{}{}
		go func(ipStr string) {
			defer wg.Done()
			defer func() { <-sem }()
			if dev := c.quickONVIFCheck(ipStr, perProbe); dev != nil {
				mu.Lock()
				results = append(results, dev)
				mu.Unlock()
			}
		}(ipStr)
	}
	wg.Wait()
	return results
}

func LocalScanCIDRs() []string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var cidrs []string
	seen := make(map[string]bool)
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip4 := ipn.IP.To4()
			if ip4 == nil {
				continue
			}

			if ip4[0] == 127 {
				continue
			}
			if ip4[0] == 172 && ip4[1] >= 16 && ip4[1] <= 31 {
				continue
			}
			if ip4[0] == 100 && (ip4[1]&0x3c) == 64 {
				continue
			}
			cidr24 := fmt.Sprintf("%d.%d.%d.0/24", ip4[0], ip4[1], ip4[2])
			if !seen[cidr24] {
				seen[cidr24] = true
				cidrs = append(cidrs, cidr24)
			}
		}
	}
	return cidrs
}

func (c *Client) SweepLocalSubnets(overall time.Duration) ([]*DeviceInfo, error) {
	return c.SweepCIDRList(LocalScanCIDRs(), overall)
}

// SweepCIDRList 对指定网段列表做单播 ONVIF 探测（容器部署下由配置显式给出宿主机可达网段）。
func (c *Client) SweepCIDRList(cidrs []string, overall time.Duration) ([]*DeviceInfo, error) {
	if len(cidrs) == 0 {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), overall)
	defer cancel()

	perProbe := 1000 * time.Millisecond
	if perProbe > overall/3 {
		perProbe = overall / 3
	}

	var mu sync.Mutex
	var wg sync.WaitGroup
	results := make([]*DeviceInfo, 0)
	seen := make(map[string]bool)

	for _, cidr := range cidrs {
		wg.Add(1)
		go func(cidr string) {
			defer wg.Done()
			if ctx.Err() != nil {
				return
			}
			devs := c.SweepCIDR(cidr, perProbe)
			mu.Lock()
			for _, dev := range devs {
				if !seen[dev.IP] {
					seen[dev.IP] = true
					results = append(results, dev)
				}
			}
			mu.Unlock()
		}(cidr)
	}
	wg.Wait()
	return results, nil
}

type EventSubscription struct {
	Address string

	TerminationTime time.Time
}

type OnvifEvent struct {
	Topic string

	UtcTime time.Time

	Items map[string]string
}

const (
	soapActionCreatePullPoint = `"http://www.onvif.org/ver10/events/wsdl/PullPointSubscription/CreatePullPointSubscriptionRequest"`
	soapActionPullMessages    = `"http://www.onvif.org/ver10/events/wsdl/PullPointSubscription/PullMessagesRequest"`
	soapActionUnsubscribe     = `"http://www.onvif.org/ver10/events/wsdl/PullPointSubscription/UnsubscribeRequest"`
)

func (c *Client) CreatePullPointSubscription(xaddr string) (*EventSubscription, error) {
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()

	body := getPullPointSubscriptionBody()
	resp, err := c.doRequest(ctx, xaddr, soapActionCreatePullPoint, body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, resp.Body)
		return nil, fmt.Errorf("failed to create event subscription (HTTP %d)", resp.StatusCode)
	}

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return parseSubscription(string(raw))
}

func (c *Client) PullMessages(subscriptionURL string, timeoutSec int) ([]OnvifEvent, error) {
	if timeoutSec <= 0 {
		timeoutSec = 10
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSec+10)*time.Second)
	defer cancel()

	origTimeout := c.httpClient.Timeout
	c.httpClient.Timeout = time.Duration(timeoutSec+10) * time.Second
	defer func() { c.httpClient.Timeout = origTimeout }()

	body := getPullMessagesBody(timeoutSec)
	resp, err := c.doRequest(ctx, subscriptionURL, soapActionPullMessages, body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, resp.Body)
		return nil, fmt.Errorf("failed to pull events (HTTP %d)", resp.StatusCode)
	}

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return parseNotificationMessages(string(raw)), nil
}

func (c *Client) Unsubscribe(subscriptionURL string) error {
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()

	body := getUnsubscribeBody()
	resp, err := c.doRequest(ctx, subscriptionURL, soapActionUnsubscribe, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	return nil
}

func getPullPointSubscriptionBody() string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope">
  <s:Body xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xmlns:xsd="http://www.w3.org/2001/XMLSchema">
    <CreatePullPointSubscription xmlns="http://www.onvif.org/ver10/events/wsdl">
      <InitialTerminationTime>PT1H</InitialTerminationTime>
    </CreatePullPointSubscription>
  </s:Body>
</s:Envelope>`
}

func getPullMessagesBody(timeoutSec int) string {

	dur := fmt.Sprintf("PT%dS", timeoutSec)
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope">
  <s:Body xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xmlns:xsd="http://www.w3.org/2001/XMLSchema">
    <PullMessages xmlns="http://www.onvif.org/ver10/events/wsdl">
      <Timeout>%s</Timeout>
      <MessageLimit>128</MessageLimit>
    </PullMessages>
  </s:Body>
</s:Envelope>`, dur)
}

func getUnsubscribeBody() string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope">
  <s:Body xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xmlns:xsd="http://www.w3.org/2001/XMLSchema">
    <Unsubscribe xmlns="http://www.onvif.org/ver10/events/wsdl"/>
  </s:Body>
</s:Envelope>`
}

var (
	reSubscriptionAddress = regexp.MustCompile(`<[^>]*(?:wsa5?:)?Address[^>]*>\s*([^<\s]+)\s*</[^>]*(?:wsa5?:)?Address>`)
	reTerminationTime     = regexp.MustCompile(`<[^>]*TerminationTime[^>]*>\s*([^<]+?)\s*</[^>]*TerminationTime>`)
	reCurrentTime         = regexp.MustCompile(`<[^>]*CurrentTime[^>]*>\s*([^<]+?)\s*</[^>]*CurrentTime>`)

	reTopic = regexp.MustCompile(`<[^>]*Topic[^>]*>\s*([^<]+?)\s*</[^>]*Topic>`)

	reNotificationMessage = regexp.MustCompile(`(?s)<[^>]*NotificationMessage[^>]*>.*?</[^>]*NotificationMessage>`)

	reSimpleItem = regexp.MustCompile(`(?s)<[^>]*SimpleItem\s+Name="([^"]+)"\s+Value="([^"]*)"`)

	reUtcTime = regexp.MustCompile(`(?s)<[^>]*Message[^>]*UtcTime="([^"]+)"`)
)

func parseSubscription(raw string) (*EventSubscription, error) {
	sub := &EventSubscription{}
	m := reSubscriptionAddress.FindStringSubmatch(raw)
	if len(m) < 2 || m[1] == "" {
		return nil, fmt.Errorf("SubscriptionReference Address not found in subscription response")
	}
	sub.Address = m[1]
	if tm := reTerminationTime.FindStringSubmatch(raw); len(tm) >= 2 {
		if t, err := parseOnvifTime(tm[1]); err == nil {
			sub.TerminationTime = t
		}
	}
	return sub, nil
}

func parseOnvifTime(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, fmt.Errorf("empty time value")
	}
	layouts := []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02T15:04:05Z0700", "2006-01-02T15:04:05.000Z"}
	for _, l := range layouts {
		if t, err := time.Parse(l, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("failed to parse time %q", s)
}

func parseNotificationMessages(raw string) []OnvifEvent {
	var events []OnvifEvent
	blocks := reNotificationMessage.FindAllString(raw, -1)
	for _, block := range blocks {
		ev := OnvifEvent{Items: make(map[string]string)}
		if m := reTopic.FindStringSubmatch(block); len(m) >= 2 {
			ev.Topic = normalizeTopic(m[1])
		}
		if m := reUtcTime.FindStringSubmatch(block); len(m) >= 2 {
			if t, err := parseOnvifTime(m[1]); err == nil {
				ev.UtcTime = t
			}
		}
		for _, sm := range reSimpleItem.FindAllStringSubmatch(block, -1) {
			if len(sm) >= 3 {
				ev.Items[sm[1]] = sm[2]
			}
		}

		if ev.Topic != "" || len(ev.Items) > 0 {
			events = append(events, ev)
		}
	}
	return events
}

func normalizeTopic(topic string) string {
	s := strings.TrimSpace(topic)

	if idx := strings.IndexByte(s, ':'); idx >= 0 && idx < 8 {
		s = s[idx+1:]
	}
	return s
}
