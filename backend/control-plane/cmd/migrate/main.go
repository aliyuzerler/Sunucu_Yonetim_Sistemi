// Command migrate applies or reverts the control plane SQL migrations.
//
// Kullanım:
//
//	migrate up      # tüm bekleyen migration'ları uygula (varsayılan)
//	migrate down    # en son uygulanan migration'ı geri al
//	migrate status  # uygulama durumunu listele
//
// Bağlantı: CP_DATABASE_URL ortam değişkeni (veya -url bayrağı).
// Migration'lar explicit'tir; otomatik çalışmaz — production'da bilinçli çağrı.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/db/migrations"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/db/migrate"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "migrate: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	cmd := "up"
	flag.Func("cmd", "up|down|status", func(s string) error {
		switch s {
		case "up", "down", "status":
			cmd = s
			return nil
		}
		return fmt.Errorf("geçersiz komut: %s", s)
	})
	url := flag.String("url", "", "PostgreSQL bağlantı adresi (varsayılan: CP_DATABASE_URL)")
	flag.Parse()

	dsn := *url
	if dsn == "" {
		dsn = os.Getenv("CP_DATABASE_URL")
	}
	if dsn == "" {
		return fmt.Errorf("bağlantı adresi yok: -url bayrağı veya CP_DATABASE_URL gerekli")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return fmt.Errorf("havuz oluşturulamadı: %w", err)
	}
	defer pool.Close()

	pairs, err := migrations.All()
	if err != nil {
		return fmt.Errorf("migration dosyaları: %w", err)
	}
	runner := &migrate.Runner{Pool: pool, Migrations: toPairs(pairs)}

	switch cmd {
	case "up":
		n, err := runner.Up(ctx)
		if err != nil {
			return err
		}
		fmt.Printf("uygulanan migration sayısı: %d\n", n)
	case "down":
		reverted, err := runner.DownOne(ctx)
		if err != nil {
			return err
		}
		if reverted {
			fmt.Println("son migration geri alındı")
		} else {
			fmt.Println("geri alınacak migration yok")
		}
	case "status":
		statuses, err := runner.Status(ctx)
		if err != nil {
			return err
		}
		fmt.Println("sürüm  ad              durum")
		for _, s := range statuses {
			state := "BEKLEMEDE"
			if s.Applied {
				state = "UYGULANDI"
			}
			fmt.Printf("%04d  %-14s %s\n", s.Version, s.Name, state)
		}
	}
	return nil
}

func toPairs(pairs []migrations.Pair) []migrate.Pair {
	out := make([]migrate.Pair, 0, len(pairs))
	for _, p := range pairs {
		out = append(out, migrate.Pair{Version: p.Version, Name: p.Name, UpSQL: p.UpSQL, DownSQL: p.DownSQL})
	}
	return out
}
