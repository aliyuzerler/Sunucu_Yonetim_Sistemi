package webops

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestValidSiteName(t *testing.T) {
	valid := []string{"a", "blog", "my-site-1", "x23"}
	invalid := []string{"", "Blog", "-lead", "trail-", "a_b", "with space", "ıde", strings.Repeat("a", 25), "a/b"}
	for _, n := range valid {
		if !ValidSiteName(n) {
			t.Errorf("ValidSiteName(%q) = false, doğru olmalı", n)
		}
	}
	for _, n := range invalid {
		if ValidSiteName(n) {
			t.Errorf("ValidSiteName(%q) = true, yanlış olmalı", n)
		}
	}
}

func TestSiteUserFitsUseraddLimit(t *testing.T) {
	longest := SiteUser(strings.Repeat("a", MaxNameLen))
	if len(longest) > 31 {
		t.Fatalf("türetilen kullanıcı adı 31 karakteri aşıyor: %s (%d)", longest, len(longest))
	}
}

func TestValidServerName(t *testing.T) {
	valid := []string{"example.com", "a.b.c.d", "localhost", "xn--trkiye-1xa.example", strings.Repeat("a", 63)}
	for _, h := range valid {
		if !ValidServerName(h) {
			t.Errorf("ValidServerName(%q) = false, doğru olmalı", h)
		}
	}
	invalid := []string{"", "EXAMPLE.com", "-a.com", "a..b", "a_b.c", "a.", ".a", "http://x.com", strings.Repeat("a", 64) + ".com"}
	for _, h := range invalid {
		if ValidServerName(h) {
			t.Errorf("ValidServerName(%q) = true, yanlış olmalı", h)
		}
	}
}

func TestValidatePHPVersion(t *testing.T) {
	for _, v := range PHPSupported {
		if err := ValidatePHPVersion(v); err != nil {
			t.Errorf("ValidatePHPVersion(%q) hata: %v", v, err)
		}
	}
	for _, v := range []string{"8.1", "7.4", "9.0", "", "8.10", "8.3 "} {
		if err := ValidatePHPVersion(v); !errors.Is(err, ErrPHPUnsupported) {
			t.Errorf("ValidatePHPVersion(%q) = %v, ErrPHPUnsupported beklenir", v, err)
		}
	}
}

func TestValidProxyTarget(t *testing.T) {
	valid := []string{"127.0.0.1:3000", "127.0.0.1:80", "[::1]:8080", "unix:/run/gunicorn/app.sock"}
	for _, s := range valid {
		if !ValidProxyTarget(s) {
			t.Errorf("ValidProxyTarget(%q) = false, doğru olmalı", s)
		}
	}
	invalid := []string{
		"10.0.0.5:3000", "0.0.0.0:80", "example.com:80", "127.0.0.1:", "127.0.0.1:99999",
		"unix:/tmp/app.sock", "unix:/run/../etc/shadow", "unix:/run/", "http://127.0.0.1:3000",
		"[::2]:8080", "",
	}
	for _, s := range invalid {
		if ValidProxyTarget(s) {
			t.Errorf("ValidProxyTarget(%q) = true, yanlış olmalı", s)
		}
	}
}

func TestSiteSpecValidate(t *testing.T) {
	ok := []SiteSpec{
		{Name: "blog", ServerNames: []string{"blog.example.com"}, Backend: BackendStatic},
		{Name: "shop", ServerNames: []string{"shop.example.com", "shop2.example.com"}, Backend: BackendPHP, PHPVersion: "8.3", MaxChildren: 20},
		{Name: "app", ServerNames: []string{"app.example.com"}, Backend: BackendProxy, ProxyTarget: "127.0.0.1:3000"},
	}
	for i, s := range ok {
		if err := s.Validate(); err != nil {
			t.Errorf("ok[%d] validate hatası: %v", i, err)
		}
	}
	bad := []struct {
		spec SiteSpec
		want error
	}{
		{SiteSpec{Name: "X", ServerNames: []string{"a.com"}, Backend: BackendStatic}, ErrInvalidName},
		{SiteSpec{Name: "ok", ServerNames: nil, Backend: BackendStatic}, ErrInvalidSpec},
		{SiteSpec{Name: "ok", ServerNames: []string{"a.com", "a.com"}, Backend: BackendStatic}, ErrInvalidSpec},
		{SiteSpec{Name: "ok", ServerNames: []string{"bad_host.com"}, Backend: BackendStatic}, ErrInvalidSpec},
		{SiteSpec{Name: "ok", ServerNames: []string{"a.com"}, Backend: "cgi"}, ErrInvalidSpec},
		{SiteSpec{Name: "ok", ServerNames: []string{"a.com"}, Backend: BackendPHP}, ErrPHPUnsupported},
		{SiteSpec{Name: "ok", ServerNames: []string{"a.com"}, Backend: BackendPHP, PHPVersion: "8.3", MaxChildren: 999}, ErrInvalidSpec},
		{SiteSpec{Name: "ok", ServerNames: []string{"a.com"}, Backend: BackendPHP, PHPVersion: "8.3", ProxyTarget: "127.0.0.1:1"}, ErrInvalidSpec},
		{SiteSpec{Name: "ok", ServerNames: []string{"a.com"}, Backend: BackendProxy}, ErrInvalidSpec},
		{SiteSpec{Name: "ok", ServerNames: []string{"a.com"}, Backend: BackendProxy, ProxyTarget: "10.1.1.1:80"}, ErrInvalidSpec},
		{SiteSpec{Name: "ok", ServerNames: []string{"a.com"}, Backend: BackendStatic, ListenPort: 70000}, ErrInvalidSpec},
		{SiteSpec{Name: "ok", ServerNames: []string{"a.com"}, Backend: BackendStatic, PHPVersion: "8.3"}, ErrInvalidSpec},
	}
	for i, tc := range bad {
		err := tc.spec.Validate()
		if !errors.Is(err, tc.want) {
			t.Errorf("bad[%d] = %v, %v beklenir", i, err, tc.want)
		}
	}
}

func TestSpecNormalizeDefaults(t *testing.T) {
	s := SiteSpec{Name: "a", ServerNames: []string{"a.com"}, Backend: BackendPHP, PHPVersion: "8.3"}.Normalize()
	if s.ListenPort != ListenPortDflt || s.MaxChildren != MaxChildrenDflt {
		t.Fatalf("normalize varsayılanları uygulanmadı: %+v", s)
	}
}

func TestRenderNginxConfigStatic(t *testing.T) {
	spec := SiteSpec{Name: "blog", ServerNames: []string{"blog.example.com", "www.blog.example.com"}, Backend: BackendStatic, ListenPort: 8080}
	out := RenderNginxConfig(spec)
	s := string(out)
	if !HasMarker(out) {
		t.Fatal("config marker ile başlamalı")
	}
	for _, want := range []string{
		metaSite + "blog", metaBackend + "static", metaPort + "8080",
		"listen 8080;", "server_name blog.example.com www.blog.example.com;",
		"root /var/www/server-control/blog/html;", "try_files $uri $uri/ =404;",
		"location ~ /\\. {", "deny all;",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("static config %q içermiyor", want)
		}
	}
	if strings.Contains(s, "fastcgi_pass") || strings.Contains(s, "proxy_pass") {
		t.Error("static config proxy/fastcgi içermemeli")
	}
}

func TestRenderNginxConfigPHP(t *testing.T) {
	spec := SiteSpec{Name: "shop", ServerNames: []string{"shop.example.com"}, Backend: BackendPHP, PHPVersion: "8.3", MaxChildren: 15}
	s := string(RenderNginxConfig(spec))
	for _, want := range []string{
		metaPHP + "8.3",
		"fastcgi_pass unix:" + PHPSocketPath("shop") + ";",
		"try_files $fastcgi_script_name =404;",
		"fastcgi_param SCRIPT_FILENAME $document_root$fastcgi_script_name;",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("php config %q içermiyor", want)
		}
	}
}

func TestRenderNginxConfigProxy(t *testing.T) {
	tcp := string(RenderNginxConfig(SiteSpec{Name: "app", ServerNames: []string{"a.com"}, Backend: BackendProxy, ProxyTarget: "127.0.0.1:3000"}))
	if !strings.Contains(tcp, "proxy_pass http://127.0.0.1:3000;") {
		t.Errorf("tcp proxy_pass yanlış: %s", tcp)
	}
	unix := string(RenderNginxConfig(SiteSpec{Name: "app", ServerNames: []string{"a.com"}, Backend: BackendProxy, ProxyTarget: "unix:/run/app/gunicorn.sock"}))
	if !strings.Contains(unix, "proxy_pass http://unix:/run/app/gunicorn.sock;") {
		t.Errorf("unix proxy_pass yanlış: %s", unix)
	}
	if !strings.Contains(unix, "proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;") {
		t.Error("proxy başlıkları eksik")
	}
}

func TestRenderPHPPool(t *testing.T) {
	spec := SiteSpec{Name: "shop", ServerNames: []string{"s.com"}, Backend: BackendPHP, PHPVersion: "8.3", MaxChildren: 10}
	out := RenderPHPPool(spec)
	s := string(out)
	if !bytes.HasPrefix(out, []byte("; "+MarkerHeader)) {
		t.Fatal("pool dosyası marker ile başlamalı")
	}
	for _, want := range []string{
		"[srvctl-shop]", "user = sc-web-shop", "group = sc-web-shop",
		"listen = " + PHPSocketPath("shop"), "listen.owner = www-data", "listen.group = www-data",
		"listen.mode = 0660", "pm = dynamic", "pm.max_children = 10",
		"disable_functions] = exec,passthru,shell_exec,system,proc_open,popen",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("pool %q içermiyor", want)
		}
	}
	// max_children=1 → sane bounds (start=1, spares 1).
	tiny := string(RenderPHPPool(SiteSpec{Name: "t", ServerNames: []string{"t.com"}, Backend: BackendPHP, PHPVersion: "8.3", MaxChildren: 1}))
	for _, want := range []string{"pm.start_servers = 1", "pm.min_spare_servers = 1", "pm.max_spare_servers = 1"} {
		if !strings.Contains(tiny, want) {
			t.Errorf("tiny pool %q içermiyor:\n%s", want, tiny)
		}
	}
}

func TestParseSiteConfigRoundTrip(t *testing.T) {
	spec := SiteSpec{Name: "blog", ServerNames: []string{"blog.example.com", "alt.example.com"}, Backend: BackendPHP, PHPVersion: "8.4", ListenPort: 8081}
	raw := RenderNginxConfig(spec)
	name, backend, php, port, sni := ParseSiteConfig(raw)
	if name != "blog" || backend != BackendPHP || php != "8.4" || port != 8081 {
		t.Fatalf("round-trip yanlış: %q %q %q %d", name, backend, php, port)
	}
	if len(sni) != 2 || sni[0] != "blog.example.com" || sni[1] != "alt.example.com" {
		t.Fatalf("server_names yanlış ayrıştırıldı: %v", sni)
	}
}

func TestParseSiteConfigForeign(t *testing.T) {
	name, _, _, _, _ := ParseSiteConfig([]byte("server {\n listen 80;\n}\n"))
	if name != "" {
		t.Fatalf("yabancı config ayrıştırılmamalı: %q", name)
	}
}

func TestParseProcNetTCP(t *testing.T) {
	body := []byte(`  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 00000000:0050 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 12345 1 ...
   1: 00000000:1F90 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 12346 1 ...
   2: 0100007F:1F91 00000000:0000 06 00000000:00000000 00:00000000 00000000     0        0 12347 1 ...
`)
	ports := ParseProcNetTCP(body)
	if len(ports) != 2 || ports[0] != 80 || ports[1] != 8080 {
		t.Fatalf("ParseProcNetTCP = %v, [80 8080] beklenir", ports)
	}
}

func TestClassifyPort(t *testing.T) {
	ls := []Listener{
		{Port: 80, Owner: "nginx"},
		{Port: 8080, Owner: "apache2"},
		{Port: 9090, Owner: "myapp"},
		{Port: 9999, Owner: ""},
	}
	cases := []struct {
		port int
		want PortOwnerVerdict
	}{
		{80, PortOwnedByNginx}, {8080, PortOwnedByApache}, {9090, PortOwnedByOther},
		{9999, PortOwnerUnknown}, {12345, PortFree},
	}
	for _, tc := range cases {
		v, _ := ClassifyPort(ls, tc.port)
		if v != tc.want {
			t.Errorf("ClassifyPort(%d) = %v, %v beklenir", tc.port, v, tc.want)
		}
	}
}

func TestSiteConflictErrorMessage(t *testing.T) {
	err := SiteConflictError(PortOwnedByApache, "apache2", 80)
	if !errors.Is(err, ErrPortConflict) || !strings.Contains(err.Error(), "edge proxy") {
		t.Fatalf("apache çakışma mesajı edge-proxy yolunu göstermeli: %v", err)
	}
	err = SiteConflictError(PortOwnerUnknown, "", 8443)
	if !errors.Is(err, ErrPortConflict) {
		t.Fatalf("bilinmeyen sahip de çakışma olmalı: %v", err)
	}
}

func TestMaxPayloadGuard(t *testing.T) {
	// Create/update payload'ı 8 KiB'yi aşamaz (mirror: ops_faz08 MaxPayload).
	if MaxPayloadBytes != 8192 {
		t.Fatalf("MaxPayloadBytes = %d", MaxPayloadBytes)
	}
}
