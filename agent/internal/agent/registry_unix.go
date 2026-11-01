//go:build unix

package agent

import (
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/broker"
)

// DefaultRegistry returns the Faz 04/06 unix executor set: the echo proof,
// the five systemd service operations, the Faz 06 read-only package
// inventory and the firewall survey/preview operations (all broker-backed
// over the unix socket; the broker runs as root).
func DefaultRegistry(brokerPath string) (*ExecutorRegistry, error) {
	return NewRegistry(
		EchoExecutor{},
		NewServiceExecutor(broker.OpServiceList, brokerPath),
		NewServiceExecutor(broker.OpServiceStatus, brokerPath),
		NewServiceExecutor(broker.OpServiceStart, brokerPath),
		NewServiceExecutor(broker.OpServiceStop, brokerPath),
		NewServiceExecutor(broker.OpServiceRestart, brokerPath),
		NewSurveyExecutor(broker.OpLinuxPkgList, brokerPath),
		NewSurveyExecutor(broker.OpLinuxFirewallStatus, brokerPath),
		NewSurveyExecutor(broker.OpLinuxFirewallRulesList, brokerPath),
		NewSurveyExecutor(broker.OpLinuxFirewallRulePreview, brokerPath),
	)
}
