// Package migrations embeds the SQL migration files so the binary can run
// them without external assets.
package migrations

import (
	"embed"
	"sort"
	"strings"
)

//go:embed *.sql
var files embed.FS

// Pair holds one versioned migration with its up/down SQL.
type Pair struct {
	Version int
	Name    string
	UpSQL   string
	DownSQL string
}

// All returns migrations sorted by version.
func All() ([]Pair, error) {
	entries, err := files.ReadDir(".")
	if err != nil {
		return nil, err
	}
	ups := map[int]Pair{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		// Naming: NNNN_name.up.sql / NNNN_name.down.sql
		base := strings.TrimSuffix(e.Name(), ".sql")
		direction := ""
		switch {
		case strings.HasSuffix(base, ".up"):
			direction = "up"
			base = strings.TrimSuffix(base, ".up")
		case strings.HasSuffix(base, ".down"):
			direction = "down"
			base = strings.TrimSuffix(base, ".down")
		default:
			continue // unrecognized file: ignore pairs strictly by convention
		}
		version, name, ok := splitVersionName(base)
		if !ok {
			continue
		}
		sql, err := files.ReadFile(e.Name())
		if err != nil {
			return nil, err
		}
		p := ups[version]
		p.Version, p.Name = version, name
		if direction == "up" {
			p.UpSQL = string(sql)
		} else {
			p.DownSQL = string(sql)
		}
		ups[version] = p
	}
	out := make([]Pair, 0, len(ups))
	for _, p := range ups {
		if p.UpSQL == "" || p.DownSQL == "" {
			return nil, errIncompletePair{version: p.Version}
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	return out, nil
}

type errIncompletePair struct{ version int }

func (e errIncompletePair) Error() string {
	return "migration çifti eksik (up/down): " + itoa(e.version)
}

func splitVersionName(base string) (int, string, bool) {
	for i := 0; i < len(base); i++ {
		if base[i] == '_' {
			v := 0
			for _, c := range base[:i] {
				if c < '0' || c > '9' {
					return 0, "", false
				}
				v = v*10 + int(c-'0')
			}
			if i == 0 {
				return 0, "", false
			}
			return v, base[i+1:], true
		}
	}
	return 0, "", false
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
