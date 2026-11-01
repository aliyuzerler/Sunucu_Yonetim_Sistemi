// Command server-agent is the node agent: it enrolls with a single-use token
// (local key, CSR) and holds an outbound mTLS gRPC stream for heartbeats,
// capability reports and typed operation delivery. It never accepts inbound
// connections and never runs shell text.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/agent"
)

func main() {
	os.Exit(runMain())
}

func usage() {
	fmt.Fprint(os.Stderr, `Kullanım:
  server-agent enroll --server <host:9090> --token <TOKEN> [--dir <dizin>] [--name <görünen ad>]
      CA sertifikası daha önce --ca-file ile dizine konmalıdır:
      cp ca.crt <dizin>/ca.crt

  server-agent run --server <host:9090> [--dir <dizin>] [--broker-endpoint <uç>]
      Outbound mTLS stream: hello/heartbeat/capability/facts/metrics +
      tipli operasyonlar. Servis işlemleri yerel broker (server-privd)
      platform uç noktası üzerinden çalışır (linux: Unix socket,
      windows: named pipe).

  server-agent version
`)
}

func dispatch(cmd string, args []string) error {
	switch cmd {
	case "version":
		fmt.Println("server-agent", agent.Version)
		return nil
	case "enroll":
		opts := parseFlags(args)
		store := &agent.Store{Dir: opts.dir}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		nodeID, err := agent.Enroll(ctx, store, opts.server, opts.token, opts.name)
		if err != nil {
			return err
		}
		fmt.Println("Kayıt başarılı.")
		fmt.Println("  node id :", nodeID)
		fmt.Println("  dizin   :", opts.dir, "(agent.key 0600, agent.crt, ca.crt)")
		fmt.Println("Şimdi: server-agent run --server", opts.server)
		return nil
	case "run":
		opts := parseFlags(args)
		store := &agent.Store{Dir: opts.dir}
		executors, err := agent.DefaultRegistry(opts.brokerSocket)
		if err != nil {
			return err
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		fmt.Println("server-agent çalışıyor (outbound mTLS):", opts.server)
		fmt.Println("  broker socket:", opts.brokerSocket)
		return agent.Run(ctx, store, opts.server, executors, agent.RunOptions{FactsRoot: ""})
	default:
		usage()
		return fmt.Errorf("bilinmeyen komut: %s", cmd)
	}
}

type options struct {
	server       string
	token        string
	dir          string
	name         string
	brokerSocket string
}

func parseFlags(args []string) options {
	o := options{dir: defaultAgentDir(), brokerSocket: defaultBrokerEndpoint()}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--server":
			i++
			if i < len(args) {
				o.server = args[i]
			}
		case "--token":
			i++
			if i < len(args) {
				o.token = args[i]
			}
		case "--dir":
			i++
			if i < len(args) {
				o.dir = args[i]
			}
		case "--name":
			i++
			if i < len(args) {
				o.name = args[i]
			}
		case "--broker-socket":
			i++
			if i < len(args) {
				o.brokerSocket = args[i]
			}
		}
	}
	return o
}
