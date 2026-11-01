package winops

import "testing"

func TestValidateServiceName(t *testing.T) {
	valid := []string{
		"Dhcp", "wuauserv", "MSSQLSERVER", "VMware Tools", "spooler_1",
		"test-service.name", "a", // tek karakter
	}
	for _, s := range valid {
		if err := ValidateServiceName(s); err != nil {
			t.Errorf("ValidateServiceName(%q) = %v, beklenen nil", s, err)
		}
	}
	invalid := []string{
		"", "-flag", "flag- ", " ", // boş / öncül tire
		`/etc/passwd`, `..\evil`, `C:\x`, // yol ayracı
		"name;rm", "a|b", "a&b", "a$b", "a`b", // shell meta
		`a"b`, `a'b`, // quote
		"a%",                        // specifier
		"trailing space ",           // sondaki boşluk
		"trailing dot.",             // sondaki nokta
		string(make([]byte, 0)), "", // tekrar boş
		"çok-uzun-" + string(make([]byte, 90)), // 80+ uzunluk (nul byte'lı ama regex reddeder)
	}
	for _, s := range invalid {
		if err := ValidateServiceName(s); err == nil {
			t.Errorf("ValidateServiceName(%q) = nil, beklenen hata", s)
		}
	}
}

func TestValidateLimit(t *testing.T) {
	if l, err := ValidateLimit(0); err != nil || l != 200 {
		t.Errorf("ValidateLimit(0) = %d,%v; beklenen 200,nil", l, err)
	}
	if _, err := ValidateLimit(501); err == nil {
		t.Error("ValidateLimit(501) hata bekler")
	}
	if _, err := ValidateLimit(-1); err == nil {
		t.Error("ValidateLimit(-1) hata bekler")
	}
	if _, err := ValidateLimit(500); err != nil {
		t.Errorf("ValidateLimit(500) = %v; beklenen nil", err)
	}
}

func TestValidateEventlogParams(t *testing.T) {
	p := EventlogParams{Channel: "System", Level: 2, Count: 100}
	if err := ValidateEventlogParams(p); err != nil {
		t.Errorf("geçerli params reddedildi: %v", err)
	}
	if p.Count != 100 {
		t.Errorf("count mutasyona uğradı: %d", p.Count)
	}
	for _, ch := range []string{"Security", "Windows PowerShell", "", "system"} {
		if err := ValidateEventlogParams(EventlogParams{Channel: ch}); err == nil {
			t.Errorf("channel %q reddedilmeli", ch)
		}
	}
	if err := ValidateEventlogParams(EventlogParams{Channel: "System", Level: 6}); err == nil {
		t.Error("level 6 reddedilmeli")
	}
	if err := ValidateEventlogParams(EventlogParams{Channel: "System", Count: 201}); err == nil {
		t.Error("count 201 reddedilmeli")
	}
}

func TestLevelName(t *testing.T) {
	cases := map[int]string{1: "critical", 2: "error", 3: "warning", 4: "information", 5: "verbose", 0: "unknown", 9: "unknown"}
	for lvl, want := range cases {
		if got := LevelName(lvl); got != want {
			t.Errorf("LevelName(%d) = %q, beklenen %q", lvl, got, want)
		}
	}
}

const wevtSample = "\xef\xbb\xbf<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n" +
	"<Events xmlns=\"http://schemas.microsoft.com/win/2004/08/events/event\">\n" +
	"<Event xmlns=\"http://schemas.microsoft.com/win/2004/08/events/event\">\n" +
	"<System><Provider Name=\"Service Control Manager\"/><EventID Qualifiers=\"16384\">7040</EventID>" +
	"<Level>4</Level><Task>0</Task><Keywords>0x8080000000000000</Keywords>" +
	"<TimeCreated SystemTime=\"2026-11-01T10:00:00.123456700Z\"/><EventRecordID>1234</EventRecordID>" +
	"<Channel>System</Channel><Computer>WIN-SRV01</Computer></System>\n" +
	"<RenderingInfo Culture=\"tr-TR\"><Message>Bilgisayar  TR-TR  mesaj\nikinci satır</Message></RenderingInfo>\n" +
	"</Event>\n" +
	"<Event><System><Provider Name=\"Microsoft-Windows-WindowsUpdateClient\"/><EventID>43</EventID>" +
	"<Level>2</Level><TimeCreated SystemTime=\"2026-11-01T11:30:00.000Z\"/><EventRecordID>1235</EventRecordID>" +
	"<Channel>System</Channel><Computer>WIN-SRV01</Computer></System></Event>\n" +
	"</Events>\n"

func TestParseWevtutilXML(t *testing.T) {
	evs := ParseWevtutilXML([]byte(wevtSample))
	if len(evs) != 2 {
		t.Fatalf("2 olay beklenir, %d geldi", len(evs))
	}
	e0 := evs[0]
	if e0.EventID != 7040 || e0.Level != 4 || e0.LevelName != "information" {
		t.Errorf("e0 alanları: %+v", e0)
	}
	if e0.Provider != "Service Control Manager" || e0.Channel != "System" || e0.Computer != "WIN-SRV01" {
		t.Errorf("e0 provider/channel/computer: %+v", e0)
	}
	if e0.RecordID != 1234 || e0.TimeCreated != "2026-11-01T10:00:00.123456700Z" {
		t.Errorf("e0 record/time: %+v", e0)
	}
	// Mesaj tek satıra inmiş olmalı.
	if e0.Message != "Bilgisayar TR-TR mesaj ikinci satır" {
		t.Errorf("e0 message = %q", e0.Message)
	}
	e1 := evs[1]
	if e1.Level != 2 || e1.LevelName != "error" || e1.Message != "" {
		t.Errorf("e1: %+v", e1)
	}
}

func TestParseWevtutilXMLMalformed(t *testing.T) {
	for _, bad := range []string{"", "<Events>", "<Events><Event><System><Level>x</Level></System></Event></Events>"} {
		if got := ParseWevtutilXML([]byte(bad)); len(got) != 0 {
			t.Errorf("bozuk girdi için boş beklenir: %q → %d", bad, len(got))
		}
	}
	// 200 sınırı.
	var b []byte
	b = append(b, []byte("<Events>")...)
	for i := 0; i < 250; i++ {
		b = append(b, []byte("<Event><System><Level>4</Level></System></Event>")...)
	}
	b = append(b, []byte("</Events>")...)
	if got := ParseWevtutilXML(b); len(got) != 200 {
		t.Errorf("200 sınırı: %d", len(got))
	}
}
