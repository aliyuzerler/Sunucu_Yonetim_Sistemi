//go:build unix

package agent

import (
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/broker"
)

// DefaultRegistry returns the Faz 04 unix executor set: the echo proof plus
// the five systemd service operations (broker-backed over the unix socket).
func DefaultRegistry(brokerPath string) (*ExecutorRegistry, error) {
	return NewRegistry(
		EchoExecutor{},
		NewServiceExecutor(broker.OpServiceList, brokerPath),
		NewServiceExecutor(broker.OpServiceStatus, brokerPath),
		NewServiceExecutor(broker.OpServiceStart, brokerPath),
		NewServiceExecutor(broker.OpServiceStop, brokerPath),
		NewServiceExecutor(broker.OpServiceRestart, brokerPath),
	)
}
