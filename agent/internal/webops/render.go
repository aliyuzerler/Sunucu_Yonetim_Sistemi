package webops

import (
	"bufio"
	"bytes"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// RenderNginxConfig produces the FULL managed site config (deterministic,
// self-contained: no external includes beyond the distro defaults). The
// marker block is machine-readable metadata used by list/status.
func RenderNginxConfig(s SiteSpec) []byte {
	s = s.Normalize()
	var b strings.Builder
	b.WriteString(MarkerHeader + "\n")
	b.WriteString(metaSite + s.Name + "\n")
	b.WriteString(metaBackend + s.Backend + "\n")
	if s.Backend == BackendPHP {
		b.WriteString(metaPHP + s.PHPVersion + "\n")
	}
	b.WriteString(metaPort + itoa(s.ListenPort) + "\n")
	b.WriteString("#\n")
	b.WriteString("server {\n")
	b.WriteString("    listen " + itoa(s.ListenPort) + ";\n")
	b.WriteString("    server_name " + strings.Join(s.ServerNames, " ") + ";\n")
	root := DocRootFor(s.Name)
	b.WriteString("    root " + root + ";\n")
	b.WriteString("    index index.html index.htm index.php;\n")
	b.WriteString("\n")
	b.WriteString("    access_log /var/log/nginx/" + ConfPrefix + s.Name + ".access.log;\n")
	b.WriteString("    error_log  /var/log/nginx/" + ConfPrefix + s.Name + ".error.log;\n")
	b.WriteString("\n")
	// Hidden files are never served (dotfile guard).
	b.WriteString("    location ~ /\\. {\n        deny all;\n    }\n")
	switch s.Backend {
	case BackendPHP:
		b.WriteString(phpLocation(s))
	case BackendProxy:
		b.WriteString(proxyLocation(s.ProxyTarget))
	default:
		b.WriteString("    location / {\n        try_files $uri $uri/ =404;\n    }\n")
	}
	b.WriteString("}\n")
	return []byte(b.String())
}

// phpLocation renders the PHP-FPM fastcgi block. Parameters are INLINE (the
// adapter does not depend on /etc/nginx/fastcgi_params existing).
func phpLocation(s SiteSpec) string {
	var b strings.Builder
	b.WriteString("    location ~ \\.php$ {\n")
	b.WriteString("        try_files $fastcgi_script_name =404;\n")
	b.WriteString("        fastcgi_pass unix:" + PHPSocketPath(s.Name) + ";\n")
	b.WriteString("        fastcgi_index index.php;\n")
	b.WriteString("        fastcgi_param SCRIPT_FILENAME $document_root$fastcgi_script_name;\n")
	b.WriteString("        fastcgi_param QUERY_STRING $query_string;\n")
	b.WriteString("        fastcgi_param REQUEST_METHOD $request_method;\n")
	b.WriteString("        fastcgi_param CONTENT_TYPE $content_type;\n")
	b.WriteString("        fastcgi_param CONTENT_LENGTH $content_length;\n")
	b.WriteString("        fastcgi_param REQUEST_URI $request_uri;\n")
	b.WriteString("        fastcgi_param DOCUMENT_ROOT $document_root;\n")
	b.WriteString("        fastcgi_param HTTPS $https;\n")
	b.WriteString("    }\n")
	return b.String()
}

// proxyLocation renders the reverse-proxy block for Node.js/Python apps.
// The target was validated loopback-only (ValidProxyTarget).
func proxyLocation(target string) string {
	scheme := "http://"
	if strings.HasPrefix(target, "unix:") {
		scheme = "http://unix:" // nginx unix upstream form: http://unix:/path
		target = strings.TrimPrefix(target, "unix:")
	} else {
		target = strings.TrimPrefix(target, scheme)
	}
	var b strings.Builder
	b.WriteString("    location / {\n")
	b.WriteString("        proxy_pass " + scheme + target + ";\n")
	b.WriteString("        proxy_http_version 1.1;\n")
	b.WriteString("        proxy_set_header Host $host;\n")
	b.WriteString("        proxy_set_header X-Real-IP $remote_addr;\n")
	b.WriteString("        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;\n")
	b.WriteString("        proxy_set_header X-Forwarded-Proto $scheme;\n")
	b.WriteString("        proxy_set_header Connection \"\";\n")
	b.WriteString("    }\n")
	return b.String()
}

// RenderPHPPool produces the per-site FPM pool: pool processes run as the
// SITE user (isolation), the socket is owned by the nginx group so only the
// web server can talk to the pool.
func RenderPHPPool(s SiteSpec) []byte {
	s = s.Normalize()
	user := SiteUser(s.Name)
	var b strings.Builder
	b.WriteString("; " + MarkerHeader + "\n")
	b.WriteString("; " + metaSite + s.Name + "\n")
	b.WriteString("; " + metaPHP + s.PHPVersion + "\n")
	b.WriteString("[srvctl-" + s.Name + "]\n")
	b.WriteString("user = " + user + "\n")
	b.WriteString("group = " + user + "\n")
	b.WriteString("listen = " + PHPSocketPath(s.Name) + "\n")
	b.WriteString("listen.owner = " + NginxGroup + "\n")
	b.WriteString("listen.group = " + NginxGroup + "\n")
	b.WriteString("listen.mode = 0660\n")
	start := 2
	minSpare, maxSpare := 1, 3
	if s.MaxChildren < start {
		start = s.MaxChildren
	}
	if maxSpare > s.MaxChildren {
		maxSpare = s.MaxChildren
	}
	if minSpare >= maxSpare {
		minSpare = maxSpare - 1
	}
	if minSpare < 1 {
		minSpare = 1
	}
	b.WriteString("pm = dynamic\n")
	b.WriteString("pm.max_children = " + itoa(s.MaxChildren) + "\n")
	b.WriteString("pm.start_servers = " + itoa(start) + "\n")
	b.WriteString("pm.min_spare_servers = " + itoa(minSpare) + "\n")
	b.WriteString("pm.max_spare_servers = " + itoa(maxSpare) + "\n")
	b.WriteString("pm.max_requests = 500\n")
	b.WriteString("php_admin_value[disable_functions] = exec,passthru,shell_exec,system,proc_open,popen\n")
	b.WriteString("chdir = " + DocRootFor(s.Name) + "\n")
	return []byte(b.String())
}

// NginxGroup is the web-server group socket ACL is granted to (Debian/Ubuntu
// default; configurable on the runner for other layouts).
const NginxGroup = "www-data"

// DocRootFor derives the FIXED document root (no override by design —
// traversal-safe: name is allowlisted).
func DocRootFor(site string) string { return "/var/www/server-control/" + site + "/html" }

// ConfFileName is the managed config path (enabled form).
func ConfFileName(dir, site string) string { return dir + "/" + ConfPrefix + site + ".conf" }

// PHPSocketPath is the per-site FPM socket.
func PHPSocketPath(site string) string { return "/run/php/" + ConfPrefix + site + ".sock" }

// PHPPoolPath is the managed pool file for one version.
func PHPPoolPath(poolDir, site string) string { return poolDir + "/" + ConfPrefix + site + ".conf" }

// PHPFPMUnit is the systemd unit name of the FPM service per version.
func PHPFPMUnit(version string) string { return "php" + version + "-fpm.service" }

// HasMarker reports whether content starts with the management marker.
// nginx configs use the bare marker; PHP-FPM pools prefix it with "; "
// (ini comment syntax) — both forms count as managed.
func HasMarker(content []byte) bool {
	t := bytes.TrimSpace(content)
	t = bytes.TrimSpace(bytes.TrimPrefix(t, []byte(";")))
	return bytes.HasPrefix(t, []byte(MarkerHeader))
}

// ParseSiteConfig extracts the metadata block of a managed config plus the
// first server_name directive (which lives INSIDE the server block — the
// whole file is scanned, but output bounds keep this cheap).
// Returns zero values when the file is not a managed one.
func ParseSiteConfig(content []byte) (name, backend, php string, port int, serverNames []string) {
	sc := bufio.NewScanner(bytes.NewReader(content))
	sc.Buffer(make([]byte, 0, 4096), 64<<10)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, metaSite):
			name = strings.TrimSpace(strings.TrimPrefix(line, metaSite))
		case strings.HasPrefix(line, metaBackend):
			backend = strings.TrimSpace(strings.TrimPrefix(line, metaBackend))
		case strings.HasPrefix(line, metaPHP):
			php = strings.TrimSpace(strings.TrimPrefix(line, metaPHP))
		case strings.HasPrefix(line, metaPort):
			port, _ = strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, metaPort)))
		case strings.HasPrefix(line, "    server_name ") && serverNames == nil:
			rest := strings.TrimSpace(strings.TrimPrefix(line, "    server_name "))
			rest = strings.TrimSuffix(rest, ";")
			for _, h := range strings.Fields(rest) {
				if h != "" {
					serverNames = append(serverNames, h)
				}
			}
		}
	}
	return name, backend, php, port, serverNames
}

// Listener describes one listening TCP socket from /proc/net/tcp{,6}.
type Listener struct {
	Port  int
	Owner string // process comm of the socket owner ("nginx", "apache2", …)
}

// ParseProcNetTCP extracts LISTEN (st == 0A) local ports from one
// /proc/net/tcp or tcp6 table body. Pure — fixture-tested.
func ParseProcNetTCP(body []byte) []int {
	sc := bufio.NewScanner(bytes.NewReader(body))
	sc.Buffer(make([]byte, 0, 4096), 1<<20)
	var ports []int
	seen := map[int]bool{}
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 4 || f[0] == "sl" {
			continue
		}
		// local_address is column 1 ("00000000:0050"); st column 3 ("0A").
		local, st := f[1], f[3]
		if st != "0A" {
			continue
		}
		_, portStr, ok := strings.Cut(local, ":")
		if !ok {
			continue
		}
		p, err := strconv.ParseUint(portStr, 16, 16)
		if err != nil || p == 0 {
			continue
		}
		if !seen[int(p)] {
			seen[int(p)] = true
			ports = append(ports, int(p))
		}
	}
	sort.Ints(ports)
	return ports
}

// FilterPortInUse reports whether port appears in the listener set.
func PortInUse(listeners []Listener, port int) bool {
	for _, l := range listeners {
		if l.Port == port {
			return true
		}
	}
	return false
}

// nginxComms are process names that legitimately serve additional vhosts on
// a shared port (name-based virtual hosting).
var nginxComms = map[string]bool{"nginx": true, "nginx:": true}

// apacheComms are the Apache driver process names (optional driver —
// designed interface point; ADR-0012).
var apacheComms = map[string]bool{"apache2": true, "httpd": true, "httpd.worker": true}

// PortOwnerVerdict classifies a port conflict.
type PortOwnerVerdict int

const (
	PortFree PortOwnerVerdict = iota
	PortOwnedByNginx
	PortOwnedByApache
	PortOwnedByOther
	PortOwnerUnknown
)

// ClassifyPort maps a listener set to a verdict for the requested port.
func ClassifyPort(listeners []Listener, port int) (PortOwnerVerdict, string) {
	for _, l := range listeners {
		if l.Port != port {
			continue
		}
		switch {
		case nginxComms[l.Owner]:
			return PortOwnedByNginx, l.Owner
		case apacheComms[l.Owner]:
			return PortOwnedByApache, l.Owner
		case l.Owner == "":
			return PortOwnerUnknown, ""
		default:
			return PortOwnedByOther, l.Owner
		}
	}
	return PortFree, ""
}

// SiteConflictError renders the typed port-conflict message (edge-proxy
// guidance included; criterion 5).
func SiteConflictError(verdict PortOwnerVerdict, owner string, port int) error {
	switch verdict {
	case PortOwnedByApache:
		return fmt.Errorf("%w: port %d Apache (%s) tarafından kullanılıyor — nginx'i edge proxy olarak farklı bir porta alın veya Apache'i o porttan çevirin (ADR-0012 §edge-proxy)", ErrPortConflict, port, owner)
	case PortOwnedByOther:
		return fmt.Errorf("%w: port %d '%s' süreci tarafından kullanılıyor", ErrPortConflict, port, owner)
	case PortOwnerUnknown:
		return fmt.Errorf("%w: port %d sahipsiz bir dinleyici tarafından kullanılıyor (sahip belirlenemedi) — güvenli taraf seçildi", ErrPortConflict, port)
	default:
		return fmt.Errorf("%w: port %d", ErrPortConflict, port)
	}
}
