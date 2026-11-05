package onvif

import "testing"

func TestExtractServiceXAddr(t *testing.T) {
	raw := `<?xml version="1.0" encoding="UTF-8"?>
<s:Envelope>
 <s:Body>
  <GetCapabilitiesResponse>
   <Capabilities>
    <Device><XAddr>http://192.168.168.201/onvif/device_service</XAddr></Device>
    <Events><XAddr>http://192.168.168.201/onvif/Events</XAddr></Events>
    <Imaging><XAddr>http://192.168.168.201/onvif/Imaging</XAddr></Imaging>
    <Media>
      <XAddr>http://192.168.168.201/onvif/Media</XAddr>
      <StreamingCapabilities/>
    </Media>
   </Capabilities>
  </GetCapabilitiesResponse>
 </s:Body>
</s:Envelope>`

	if got := extractServiceXAddr(raw, "Media"); got != "http://192.168.168.201/onvif/Media" {
		t.Errorf("Media XAddr 解析错误: %q", got)
	}
	if got := extractServiceXAddr(raw, "Device"); got != "http://192.168.168.201/onvif/device_service" {
		t.Errorf("Device XAddr 解析错误: %q", got)
	}
	if got := extractServiceXAddr(raw, "Events"); got != "http://192.168.168.201/onvif/Events" {
		t.Errorf("Events XAddr 解析错误: %q", got)
	}
}

func TestExtractServiceXAddrWithPrefix(t *testing.T) {

	raw := `<tt:Capabilities>
  <tt:Media><tt:XAddr>http://192.168.168.202/onvif/Media</tt:XAddr></tt:Media>
  <tt:Events><tt:XAddr>http://192.168.168.202/onvif/Events</tt:XAddr></tt:Events>
</tt:Capabilities>`

	if got := extractServiceXAddr(raw, "Media"); got != "http://192.168.168.202/onvif/Media" {
		t.Errorf("带前缀 Media XAddr 解析错误: %q", got)
	}
	if got := extractServiceXAddr(raw, "Events"); got != "http://192.168.168.202/onvif/Events" {
		t.Errorf("带前缀 Events XAddr 解析错误: %q", got)
	}
}
