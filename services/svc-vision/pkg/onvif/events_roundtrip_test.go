package onvif

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEventServiceRoundTrip(t *testing.T) {
	mux := http.NewServeMux()

	mux.HandleFunc("/onvif/device_service", func(w http.ResponseWriter, r *http.Request) {
		soapAction := r.Header.Get("SOAPAction")
		if strings.Contains(soapAction, "CreatePullPointSubscription") {
			resp := `<?xml version="1.0" encoding="UTF-8"?>
<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope">
 <s:Body>
  <CreatePullPointSubscriptionResponse xmlns="http://www.onvif.org/ver10/events/wsdl">
   <SubscriptionReference xmlns="http://www.onvif.org/ver10/schema">
    <wsa5:Address xmlns:wsa5="http://www.w3.org/2005/08/addressing">/onvif/events?Idx=1</wsa5:Address>
   </SubscriptionReference>
   <CurrentTime xmlns="http://docs.oasis-open.org/wsn/b-2">2026-08-28T16:00:00Z</CurrentTime>
   <TerminationTime xmlns="http://docs.oasis-open.org/wsn/b-2">2026-08-28T17:00:00Z</TerminationTime>
  </CreatePullPointSubscriptionResponse>
 </s:Body>
</s:Envelope>`
			w.Header().Set("Content-Type", "application/soap+xml")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(resp))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})

	mux.HandleFunc("/onvif/events", func(w http.ResponseWriter, r *http.Request) {
		soapAction := r.Header.Get("SOAPAction")
		if !strings.Contains(soapAction, "PullMessages") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = io.ReadAll(r.Body)
		resp := `<?xml version="1.0" encoding="UTF-8"?>
<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope">
 <s:Body>
  <PullMessagesResponse xmlns="http://www.onvif.org/ver10/events/wsdl">
   <CurrentTime xmlns="http://docs.oasis-open.org/wsn/b-2">2026-08-28T16:00:10Z</CurrentTime>
   <TerminationTime xmlns="http://docs.oasis-open.org/wsn/b-2">2026-08-28T17:00:00Z</TerminationTime>
   <NotificationMessage xmlns="http://docs.oasis-open.org/wsn/b-2">
    <Topic Dialect="http://www.onvif.org/ver10/tev/topicExpression/ConcreteSet">
      tns1:RuleEngine/MotionRegionDetector/Motion
    </Topic>
    <Message>
      <tt:Message xmlns:tt="http://www.onvif.org/ver10/schema" UtcTime="2026-08-28T16:00:05Z" PropertyOperation="Changed">
       <tt:Data>
        <tt:SimpleItem Name="IsMotion" Value="true"/>
       </tt:Data>
      </tt:Message>
    </Message>
   </NotificationMessage>
  </PullMessagesResponse>
 </s:Body>
</s:Envelope>`
		w.Header().Set("Content-Type", "application/soap+xml")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(resp))
	})

	ts := httptest.NewServer(mux)
	defer ts.Close()

	client := NewClient(5)
	client.SetCredentials("admin", "123456")

	sub, err := client.CreatePullPointSubscription(ts.URL + "/onvif/device_service")
	if err != nil {
		t.Fatalf("订阅失败: %v", err)
	}

	addr := sub.Address
	if strings.HasPrefix(addr, "/") {
		addr = ts.URL + addr
	}
	t.Logf("订阅地址: %s", addr)

	events, err := client.PullMessages(addr, 1)
	if err != nil {
		t.Fatalf("拉取事件失败: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("应拉取到 1 个事件，实际 %d", len(events))
	}
	if events[0].Topic != "RuleEngine/MotionRegionDetector/Motion" {
		t.Errorf("Topic 错误: %q", events[0].Topic)
	}
	if events[0].Items["IsMotion"] != "true" {
		t.Errorf("IsMotion 错误: %q", events[0].Items["IsMotion"])
	}
	fmt.Println("roundtrip ok")
}
