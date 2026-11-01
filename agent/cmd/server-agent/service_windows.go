//go:build windows

// Windows service integration for server-agent (Faz 05). `svc-run` is the
// SCM entry point: `sc create server-agent binPath= "…server-agent.exe
// svc-run --server host:9090 …"`. CLI usage is unchanged on windows.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/agent"
	"golang.org/x/sys/windows/svc"
)

const svcName = "server-agent"

// runMain is the windows entry: intercept `svc-run` (SCM launches the
// binary with that argument), otherwise behave like the CLI.
func runMain() int {
	if len(os.Args) >= 2 && os.Args[1] == "svc-run" {
		isService, err := svc.IsWindowsService()
		if err != nil {
			fmt.Fprintln(os.Stderr, "svc.IsWindowsService:", err)
			return 1
		}
		if !isService {
			fmt.Fprintln(os.Stderr, "svc-run yalnızca Windows Servis Denetim Yöneticisi tarafından kullanılır")
			return 2
		}
		// SCM argümanları: ["svc-run", <kalan CLI argümanları…>]
		rest := os.Args[2:]
		if err := svc.Run(svcName, &agentService{args: rest}); err != nil {
			fmt.Fprintln(os.Stderr, "svc.Run:", err)
			return 1
		}
		return 0
	}
	if len(os.Args) < 2 {
		usage()
		return 2
	}
	if err := dispatch(os.Args[1], os.Args[2:]); err != nil {
		fmt.Fprintf(os.Stderr, "server-agent %s: %v\n", os.Args[1], err)
		return 1
	}
	return 0
}

// agentService implements svc.Handler: it forwards to agent.Run with an
// SCM-driven context.
type agentService struct{ args []string }

func (s *agentService) Execute(_ []string, req <-chan svc.ChangeRequest, changes chan<- svc.Status) (bool, uint32) {
	const accepts = svc.AcceptStop | svc.AcceptShutdown
	changes <- svc.Status{State: svc.StartPending}
	opts := parseFlags(s.args)
	store := &agent.Store{Dir: opts.dir}
	executors, err := agent.DefaultRegistry(opts.brokerSocket, opts.dockerSocket)
	if err != nil {
		changes <- svc.Status{State: svc.Stopped, Win32ExitCode: 1}
		return false, 1
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		// SCM süreci tutar; Run bir stream hatasında kendisi yeniden
		// bağlanır (exponential backoff) — burada ctx iptalini bekler.
		err := agent.Run(ctx, store, opts.server, executors, agent.RunOptions{FactsRoot: ""})
		done <- err
	}()
	changes <- svc.Status{State: svc.Running, Accepts: accepts}

	for c := range req {
		switch c.Cmd {
		case svc.Interrogate:
			changes <- c.CurrentStatus
		case svc.Stop, svc.Shutdown:
			changes <- svc.Status{State: svc.StopPending}
			cancel()
			<-done
			changes <- svc.Status{State: svc.Stopped}
			return false, 0
		}
	}
	cancel()
	<-done
	return false, 0
}

// defaultAgentDir is the windows agent state dir.
func defaultAgentDir() string { return `C:\ProgramData\server-control-agent` }

// defaultBrokerEndpoint is the windows broker pipe path.
func defaultBrokerEndpoint() string { return `\\.\pipe\server-control-privd` }
