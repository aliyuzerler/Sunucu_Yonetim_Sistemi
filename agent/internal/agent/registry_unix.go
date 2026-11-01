//go:build unix

package agent

import (
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/broker"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/composeops"
)

// DefaultRegistry returns the Faz 04/06/07 unix executor set: the echo
// proof, the systemd service operations, the read-only package inventory,
// the firewall survey/preview family, the Docker Engine family (broker-only
// socket access; preview-only removes — ADR-0011) and the compose analysis
// family (agent-local, pure). dockerSocket is the PROBE path only; the agent
// never dials the Engine socket itself.
func DefaultRegistry(brokerPath, dockerSocket string) (*ExecutorRegistry, error) {
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
		// Faz 07: Docker Engine ailesi (13 op — 9 yürütülebilir + 4 preview).
		NewDockerExecutor(broker.OpDockerContainerList, brokerPath, dockerSocket),
		NewDockerExecutor(broker.OpDockerContainerInspect, brokerPath, dockerSocket),
		NewDockerExecutor(broker.OpDockerContainerLogs, brokerPath, dockerSocket),
		NewDockerExecutor(broker.OpDockerContainerStart, brokerPath, dockerSocket),
		NewDockerExecutor(broker.OpDockerContainerStop, brokerPath, dockerSocket),
		NewDockerExecutor(broker.OpDockerContainerRestart, brokerPath, dockerSocket),
		NewDockerExecutor(broker.OpDockerContainerRemove, brokerPath, dockerSocket),
		NewDockerExecutor(broker.OpDockerImageList, brokerPath, dockerSocket),
		NewDockerExecutor(broker.OpDockerImageRemove, brokerPath, dockerSocket),
		NewDockerExecutor(broker.OpDockerNetworkList, brokerPath, dockerSocket),
		NewDockerExecutor(broker.OpDockerNetworkRemove, brokerPath, dockerSocket),
		NewDockerExecutor(broker.OpDockerVolumeList, brokerPath, dockerSocket),
		NewDockerExecutor(broker.OpDockerVolumeRemove, brokerPath, dockerSocket),
		// Faz 07: Compose analizi (agent-local saf).
		NewComposeExecutor(composeops.OpDockerComposeValidate),
		NewComposeExecutor(composeops.OpDockerComposeDeploy),
	)
}
