package onvif

import (
	"testing"
)

func TestParseSubscription(t *testing.T) {
	raw := `<?xml version="1.0" encoding="UTF-8"?>
<s:Envelope>
 <s:Body>
  <CreatePullPointSubscriptionResponse>
   <SubscriptionReference>
    <wsa5:Address>http://192.168.168.202/onvif/event_service?Idx=1</wsa5:Address>
   </SubscriptionReference>
   <wsnt:CurrentTime>2026-08-28T16:00:00Z</wsnt:CurrentTime>
   <wsnt:TerminationTime>2026-08-28T17:00:00Z</wsnt:TerminationTime>
  </CreatePullPointSubscriptionResponse>
 </s:Body>
</s:Envelope>`

	sub, err := parseSubscription(raw)
	if err != nil {
		t.Fatalf("解析订阅失败: %v", err)
	}
	if sub.Address != "http://192.168.168.202/onvif/event_service?Idx=1" {
		t.Errorf("Address 解析错误: %q", sub.Address)
	}
	if sub.TerminationTime.IsZero() {
		t.Errorf("TerminationTime 应被解析")
	}
}

func TestParseNotificationMessages(t *testing.T) {
	raw := `<?xml version="1.0" encoding="UTF-8"?>
<s:Envelope>
 <s:Body>
  <tev:PullMessagesResponse>
   <tev:CurrentTime>2026-08-28T16:00:10Z</tev:CurrentTime>
   <tev:TerminationTime>2026-08-28T17:00:00Z</tev:TerminationTime>
   <wsnt:NotificationMessage>
    <wsnt:Topic Dialect="http://www.onvif.org/ver10/tev/topicExpression/ConcreteSet">
      tns1:RuleEngine/MotionRegionDetector/Motion
    </wsnt:Topic>
    <wsnt:Message>
      <tt:Message UtcTime="2026-08-28T16:00:05Z" PropertyOperation="Changed">
       <tt:Source>
        <tt:SimpleItem Name="VideoSourceConfigurationToken" Value="vsrc"/>
       </tt:Source>
       <tt:Data>
        <tt:SimpleItem Name="IsMotion" Value="true"/>
        <tt:SimpleItem Name="MotionStatus" Value="Motion"/>
       </tt:Data>
      </tt:Message>
    </wsnt:Message>
   </wsnt:NotificationMessage>
   <wsnt:NotificationMessage>
    <wsnt:Topic Dialect="http://www.onvif.org/ver10/tev/topicExpression/ConcreteSet">
      tns1:RuleEngine/LineDetector/Crossed
    </wsnt:Topic>
    <wsnt:Message>
      <tt:Message UtcTime="2026-08-28T16:00:08Z">
       <tt:Data>
        <tt:SimpleItem Name="IsCrossed" Value="true"/>
       </tt:Data>
      </tt:Message>
    </wsnt:Message>
   </wsnt:NotificationMessage>
  </tev:PullMessagesResponse>
 </s:Body>
</s:Envelope>`

	events := parseNotificationMessages(raw)
	if len(events) != 2 {
		t.Fatalf("应解析出 2 个事件，实际 %d", len(events))
	}

	if events[0].Topic != "RuleEngine/MotionRegionDetector/Motion" {
		t.Errorf("事件0 Topic 错误: %q", events[0].Topic)
	}
	if events[0].Items["IsMotion"] != "true" {
		t.Errorf("事件0 IsMotion 错误: %q", events[0].Items["IsMotion"])
	}
	if events[0].UtcTime.IsZero() {
		t.Errorf("事件0 UtcTime 应被解析")
	}
	if events[1].Topic != "RuleEngine/LineDetector/Crossed" {
		t.Errorf("事件1 Topic 错误: %q", events[1].Topic)
	}
}

func TestNormalizeTopic(t *testing.T) {
	cases := map[string]string{
		"tns1:RuleEngine/MotionRegionDetector/Motion": "RuleEngine/MotionRegionDetector/Motion",
		"RuleEngine/MotionRegionDetector/Motion":      "RuleEngine/MotionRegionDetector/Motion",
		"v:Engine/Motion":                             "Engine/Motion",
	}
	for in, want := range cases {
		if got := normalizeTopic(in); got != want {
			t.Errorf("normalizeTopic(%q) = %q, 期望 %q", in, got, want)
		}
	}
}
