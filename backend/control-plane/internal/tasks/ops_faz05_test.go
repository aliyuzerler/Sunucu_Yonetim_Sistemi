package tasks

import (
	"encoding/json"
	"testing"
)

// Windows ailesi payload doğrulayıcıları — pozitif + negatif setler.
func TestValidateWinServiceName(t *testing.T) {
	valid := []string{"Dhcp", "wuauserv", "MSSQLSERVER", "VMware Tools", "spooler_1"}
	for _, v := range valid {
		payload, _ := json.Marshal(serviceWinPayload{Service: v})
		if err := ValidateWinServiceName(payload); err != nil {
			t.Errorf("ValidateWinServiceName(%q) = %v, beklenen nil", v, err)
		}
	}
	invalid := []string{
		"", "-flag", `..\evil`, `C:\x`, "a|b", "a&b", `a"b`, "a%;",
		"trailing space ", "trailing dot.", string(make([]byte, 120)),
	}
	for _, v := range invalid {
		payload, _ := json.Marshal(serviceWinPayload{Service: v})
		if err := ValidateWinServiceName(payload); err == nil {
			t.Errorf("ValidateWinServiceName(%q) nil, hata beklenir", v)
		}
	}
	// Şema ihlalleri.
	if err := ValidateWinServiceName(json.RawMessage(`{"unit":"Dhcp"}`)); err == nil {
		t.Error("bilinmeyen alan reddedilmeli")
	}
	if err := ValidateWinServiceName(json.RawMessage(`{}`)); err == nil {
		t.Error("boş servis adı reddedilmeli")
	}
}

func TestValidateWinServiceList(t *testing.T) {
	if err := ValidateWinServiceList(nil); err != nil {
		t.Errorf("boş payload geçerli: %v", err)
	}
	if err := ValidateWinServiceList(json.RawMessage(`{"limit":100}`)); err != nil {
		t.Errorf("limit 100 geçerli: %v", err)
	}
	if err := ValidateWinServiceList(json.RawMessage(`{"limit":501}`)); err == nil {
		t.Error("limit 501 reddedilmeli")
	}
}

func TestValidateEventlogRead(t *testing.T) {
	valid := []eventlogPayload{
		{Channel: "Application"},
		{Channel: "System", Level: 2, Count: 100},
		{Channel: "Setup", Count: 1},
	}
	for _, v := range valid {
		payload, _ := json.Marshal(v)
		if err := ValidateEventlogRead(payload); err != nil {
			t.Errorf("ValidateEventlogRead(%+v) = %v, beklenen nil", v, err)
		}
	}
	invalid := []eventlogPayload{
		{Channel: "Security"},           // tasarımdan hariç (ADR-0009)
		{Channel: "Windows PowerShell"}, // enum dışı
		{Channel: "system"},             // case-sensitive enum
		{Channel: "System", Level: 6},   // level sınırı
		{Channel: "System", Count: 201}, // count sınırı
	}
	for _, v := range invalid {
		payload, _ := json.Marshal(v)
		if err := ValidateEventlogRead(payload); err == nil {
			t.Errorf("ValidateEventlogRead(%+v) nil, hata beklenir", v)
		}
	}
	if err := ValidateEventlogRead(json.RawMessage(`{"channel":"System","xpath":"*"}`)); err == nil {
		t.Error("bilinmeyen alan (xpath) reddedilmeli — injection yok")
	}
}
