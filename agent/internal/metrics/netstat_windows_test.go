//go:build !linux

package metrics

import "testing"

// TestParseIfTableFixture walks a hand-built MIB_IFTABLE buffer. The layout
// constants (stride 856, offsets 516/544/552/556) MUST match the Windows
// SDK MIB_IFROW; this fixture test guards the parser on every GOOS.
func TestParseIfTableFixture(t *testing.T) {
	// 2 satır: ethernet aktif + loopback (atlanır).
	buf := make([]byte, 4+2*mibIfRowSize)
	putU32(buf[0:4], 2)

	row0 := buf[4 : 4+mibIfRowSize]
	putU32(row0[516:], 6)  // dwType = ethernetCsmacd
	putU32(row0[544:], 5)  // dwOperStatus = OPERATIONAL
	putU32(row0[552:], 42) // dwInOctets
	putU32(row0[556:], 99) // dwOutOctets
	// wszName: "Ethernet"
	for i, ch := range []uint16{'E', 't', 'h', 'e', 'r', 'n', 'e', 't', 0} {
		putU16(row0[2*i:], ch)
	}

	row1 := buf[4+mibIfRowSize:]
	putU32(row1[516:], ifTypeLoopback) // loopback → atlanır
	putU32(row1[544:], 5)

	nets := parseIfTable(buf)
	if len(nets) != 1 {
		t.Fatalf("1 arayüz beklenir, %d geldi", len(nets))
	}
	n := nets[0]
	if n.Interface != "Ethernet" || n.RxBytes != 42 || n.TxBytes != 99 {
		t.Errorf("arayüz: %+v", n)
	}
}

func TestParseIfTableMalformed(t *testing.T) {
	for _, bad := range [][]byte{nil, {0}, {1, 2, 3}} {
		if got := parseIfTable(bad); len(got) != 0 {
			t.Errorf("bozuk buffer boş dönmeli: %v", got)
		}
	}
	// n alanı satır alanından büyükse güvenli dönüş.
	buf := make([]byte, 4)
	putU32(buf, 1000)
	if got := parseIfTable(buf); len(got) != 0 {
		t.Errorf("taşan n reddedilmeli: %d", len(got))
	}
}

func TestIfRowNameFallback(t *testing.T) {
	row := make([]byte, mibIfRowSize)
	putU32(row[512:], 17) // dwIndex
	putU16(row[0:], 0)    // boş isim → "if17"
	if got := ifRowName(row); got != "if17" {
		t.Errorf("ifRowName = %q, beklenen if17", got)
	}
	// Yazdırılamayan karakter → index fallback.
	putU16(row[0:], 0x00E9) // é (121 > 126 değil ama >126 şartı için 0x2000 kullan)
	putU16(row[0:], 0x2000)
	if got := ifRowName(row); got != "if17" {
		t.Errorf("yazdırılamayan isim fallback: %q", got)
	}
}

func putU32(b []byte, v uint32) {
	b[0] = byte(v)
	b[1] = byte(v >> 8)
	b[2] = byte(v >> 16)
	b[3] = byte(v >> 24)
}

func putU16(b []byte, v uint16) {
	b[0] = byte(v)
	b[1] = byte(v >> 8)
}
