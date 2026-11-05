package onvifevent

import (
	"testing"

	"platfarm/svc-vision/internal/models"
	"platfarm/svc-vision/pkg/onvif"
)

func TestMapTopicToAlert(t *testing.T) {
	cases := []struct {
		topic string
		want  string
	}{
		{"RuleEngine/MotionRegionDetector/Motion", models.AlertTypeMotion},
		{"RuleEngine/LineDetector/Crossed", models.AlertTypeLineCross},
		{"VideoSource/MotionAlarm", models.AlertTypeMotion},
		{"VideoAnalytics/Motion", models.AlertTypeMotion},
		{"RuleEngine/FieldDetector/ObjectsInside", models.AlertTypeIntrusion},
		{"Device/Trigger/DigitalInput", models.AlertTypeIntrusion},
		{"Device/Hardware/Tamper", models.AlertTypeObjectDetect},
		{"Device/Network/Disconnect", models.AlertTypeOffline},
	}
	for _, c := range cases {
		got, _, isAlarm := mapTopicToAlert(c.topic)
		if !isAlarm {
			t.Errorf("mapTopicToAlert(%q) 期望为报警事件", c.topic)
			continue
		}
		if got != c.want {
			t.Errorf("mapTopicToAlert(%q) = %q, 期望 %q", c.topic, got, c.want)
		}
	}
}

func TestMapTopicNonAlarmIgnored(t *testing.T) {
	cases := []string{
		"Monitoring/ProcessorUsage",
		"Monitoring/MemoryUsage",
		"Device/IO/SomeStats",
	}
	for _, topic := range cases {
		if _, _, isAlarm := mapTopicToAlert(topic); isAlarm {
			t.Errorf("非报警主题 %q 应被忽略，但被当作报警", topic)
		}
	}

	if _, _, isAlarm := mapTopicToAlert("SomeUnknown/Topic"); isAlarm {
		t.Errorf("未知主题不应被当作报警")
	}
}

func TestMapTopicLevel(t *testing.T) {

	if _, lvl, _ := mapTopicToAlert("RuleEngine/LineDetector/Crossed"); lvl != models.AlertLevelHigh {
		t.Errorf("越线报警等级应为 high, got %s", lvl)
	}
	if _, lvl, _ := mapTopicToAlert("RuleEngine/MotionRegionDetector/Motion"); lvl != models.AlertLevelMedium {
		t.Errorf("运动报警等级应为 medium, got %s", lvl)
	}
}

func TestBuildDetailsEmpty(t *testing.T) {
	ev := onvif.OnvifEvent{Topic: "t/RuleEngine/Motion", Items: map[string]string{}}
	d := buildDetails(ev)
	if d != `{"topic": "t/RuleEngine/Motion"}` {
		t.Errorf("buildDetails 空 items 期望纯 topic，got %s", d)
	}
}
