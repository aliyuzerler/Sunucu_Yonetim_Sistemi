//go:build unix

package agent

import (
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/broker"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/composeops"
)

// DefaultRegistry returns the Faz 04/06/07/08/09 unix executor set: the echo
// proof, the systemd service operations, the read-only package inventory,
// the firewall survey/preview family, the Docker Engine family (broker-only
// socket access; preview-only removes — ADR-0011), the compose analysis
// family (agent-local, pure) and the web hosting family (broker-only real
// execution behind --web-enable — ADR-0012). dockerSocket is the PROBE path
// only; the agent never dials the Engine socket itself.
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
		// Faz 08: Web hosting ailesi (7 op — gerçek yürütme yalnız broker'da
		// --web-enable ile; ADR-0012).
		NewWebExecutor(broker.OpWebSiteList, brokerPath),
		NewWebExecutor(broker.OpWebSiteStatus, brokerPath),
		NewWebExecutor(broker.OpWebSiteCreate, brokerPath),
		NewWebExecutor(broker.OpWebSiteUpdate, brokerPath),
		NewWebExecutor(broker.OpWebSiteDisable, brokerPath),
		NewWebExecutor(broker.OpWebSiteDelete, brokerPath),
		NewWebExecutor(broker.OpWebPHPList, brokerPath),
		// Faz 09: TLS sertifika ailesi (3 op — gerçek yürütme yalnız
		// broker'da --web-enable ile; ADR-0013).
		NewWebExecutor(broker.OpWebCertRequest, brokerPath),
		NewWebExecutor(broker.OpWebCertInstall, brokerPath),
		NewWebExecutor(broker.OpWebCertStatus, brokerPath),
	)
}
