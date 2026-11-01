package tasks

import (
	"encoding/json"
	"testing"
)

// Faz 06 validator pozitif + negatif setleri (ops_faz06.go aynaları).
func TestValidatePkgList(t *testing.T) {
	if err := ValidatePkgList(nil); err != nil {
		t.Errorf("boş payload geçerli: %v", err)
	}
	if err := ValidatePkgList(json.RawMessage(`{"limit":100}`)); err != nil {
		t.Errorf("limit 100 geçerli: %v", err)
	}
	if err := ValidatePkgList(json.RawMessage(`{"limit":501}`)); err == nil {
		t.Error("limit 501 reddedilmeli")
	}
	if err := ValidatePkgList(json.RawMessage(`{"limit":-3}`)); err == nil {
		t.Error("negatif limit reddedilmeli")
	}
	if err := ValidatePkgList(json.RawMessage(`{"name":"bash"}`)); err == nil {
		t.Error("bilinmeyen alan reddedilmeli")
	}
}

func TestValidateFirewallStatus(t *testing.T) {
	if err := ValidateFirewallStatus(nil); err != nil {
		t.Errorf("boş payload geçerli: %v", err)
	}
	if err := ValidateFirewallStatus(json.RawMessage(`{}`)); err != nil {
		t.Errorf("boş obje geçerli: %v", err)
	}
	if err := ValidateFirewallStatus(json.RawMessage(`{"zone":"public"}`)); err == nil {
		t.Error("bilinmeyen alan reddedilmeli")
	}
}

func TestValidateFirewallPreview(t *testing.T) {
	valid := []fwPreviewPayload{
		{Port: 8080, Protocol: "tcp", Action: "allow"},
		{Zone: "public", Port: 53, Protocol: "udp", Action: "revoke"},
	}
	for _, v := range valid {
		payload, _ := json.Marshal(v)
		if err := ValidateFirewallPreview(payload); err != nil {
			t.Errorf("geçerli preview reddedildi: %+v → %v", v, err)
		}
	}
	invalid := []fwPreviewPayload{
		{Port: 0, Protocol: "tcp", Action: "allow"},
		{Port: 65536, Protocol: "tcp", Action: "allow"},
		{Port: 80, Protocol: "sctp", Action: "allow"},
		{Port: 80, Protocol: "tcp", Action: "drop"},
		{Port: 80, Protocol: "tcp", Action: "allow", Zone: "bad;zone"},
		{Port: 80, Protocol: "tcp", Action: "allow", Zone: "a b"},
	}
	for _, v := range invalid {
		payload, _ := json.Marshal(v)
		if err := ValidateFirewallPreview(payload); err == nil {
			t.Errorf("geçersiz preview kabul edildi: %+v", v)
		}
	}
	// Şema ihlali: xpath benzeri bilinmeyen alan enjeksiyon yüzeyi değildir.
	if err := ValidateFirewallPreview(json.RawMessage(`{"port":80,"protocol":"tcp","action":"allow","commands":"x"}`)); err == nil {
		t.Error("bilinmeyen alan (commands) reddedilmeli")
	}
}
