package controller

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestShopDashboardJSONIsValid(t *testing.T) {
	out := shopDashboardJSON("baki", "default")

	var dashboard map[string]interface{}
	if err := json.Unmarshal([]byte(out), &dashboard); err != nil {
		t.Fatalf("rendered dashboard is not valid JSON: %v", err)
	}

	if dashboard["uid"] != "shop-baki" {
		t.Errorf("uid: want shop-baki, got %v", dashboard["uid"])
	}
	if !strings.Contains(out, `shop=\"baki\"`) {
		t.Error("dashboard queries are not scoped to shop=\"baki\"")
	}
	if strings.Contains(out, "__SHOP__") || strings.Contains(out, "__NS__") {
		t.Error("unsubstituted placeholders remain in rendered dashboard")
	}
	if panels, ok := dashboard["panels"].([]interface{}); !ok || len(panels) == 0 {
		t.Error("dashboard has no panels")
	}
}

func TestPrometheusRuleAndDashboardNames(t *testing.T) {
	if got := prometheusRuleName("baki"); got != "baki-alerts" {
		t.Errorf("prometheusRuleName: got %s", got)
	}
	if got := dashboardCMName("baki"); got != "baki-dashboard" {
		t.Errorf("dashboardCMName: got %s", got)
	}
	if got := discordWebhookSecret("baki"); got != "baki-discord-webhook" {
		t.Errorf("discordWebhookSecret: got %s", got)
	}
}
