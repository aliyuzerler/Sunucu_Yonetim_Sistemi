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
		// Faz 10: veritabanı ailesi (11 op — gerçek yürütme yalnız
		// broker'da --db-enable ile; ADR-0014).
		NewDbExecutor(broker.OpDBInstanceList, brokerPath),
		NewDbExecutor(broker.OpDBDatabaseList, brokerPath),
		NewDbExecutor(broker.OpDBDatabaseCreate, brokerPath),
		NewDbExecutor(broker.OpDBDatabaseDelete, brokerPath),
		NewDbExecutor(broker.OpDBUserList, brokerPath),
		NewDbExecutor(broker.OpDBUserCreate, brokerPath),
		NewDbExecutor(broker.OpDBUserDelete, brokerPath),
		NewDbExecutor(broker.OpDBGrantSet, brokerPath),
		NewDbExecutor(broker.OpDBBackup, brokerPath),
		NewDbExecutor(broker.OpDBBackupList, brokerPath),
		NewDbExecutor(broker.OpDBRestore, brokerPath),
		// Faz 10: scoped dosya yöneticisi (5 op — gerçek yürütme yalnız
		// broker'da --fs-enable ile; ADR-0014).
		NewFsExecutor(broker.OpFSList, brokerPath),
		NewFsExecutor(broker.OpFSDownload, brokerPath),
		NewFsExecutor(broker.OpFSUpload, brokerPath),
		NewFsExecutor(broker.OpFSChmod, brokerPath),
		NewFsExecutor(broker.OpFSChown, brokerPath),
		// Faz 11: e-posta servisi ailesi (16 op — gerçek yürütme yalnız
		// broker'da --mail-enable ile; ADR-0015).
		NewMailExecutor(broker.OpMailPreflight, brokerPath),
		NewMailExecutor(broker.OpMailStatus, brokerPath),
		NewMailExecutor(broker.OpMailDomainList, brokerPath),
		NewMailExecutor(broker.OpMailDomainCreate, brokerPath),
		NewMailExecutor(broker.OpMailDomainDelete, brokerPath),
		NewMailExecutor(broker.OpMailBoxList, brokerPath),
		NewMailExecutor(broker.OpMailBoxCreate, brokerPath),
		NewMailExecutor(broker.OpMailBoxDelete, brokerPath),
		NewMailExecutor(broker.OpMailAliasList, brokerPath),
		NewMailExecutor(broker.OpMailAliasCreate, brokerPath),
		NewMailExecutor(broker.OpMailAliasDelete, brokerPath),
		NewMailExecutor(broker.OpMailQuotaSet, brokerPath),
		NewMailExecutor(broker.OpMailConfigCheck, brokerPath),
		NewMailExecutor(broker.OpMailQueueList, brokerPath),
		NewMailExecutor(broker.OpMailLimitsGet, brokerPath),
		NewMailExecutor(broker.OpMailLimitsSet, brokerPath),
		// Faz 12: KVM/QEMU/libvirt sanallaştırma ailesi (16 op — gerçek
		// yürütme yalnız broker'da --vm-enable ile; ADR-0016).
		NewVmExecutor(broker.OpVMPreflight, brokerPath),
		NewVmExecutor(broker.OpVMStatus, brokerPath),
		NewVmExecutor(broker.OpVMList, brokerPath),
		NewVmExecutor(broker.OpVMInfo, brokerPath),
		NewVmExecutor(broker.OpVMCreate, brokerPath),
		NewVmExecutor(broker.OpVMClone, brokerPath),
		NewVmExecutor(broker.OpVMStart, brokerPath),
		NewVmExecutor(broker.OpVMStop, brokerPath),
		NewVmExecutor(broker.OpVMReboot, brokerPath),
		NewVmExecutor(broker.OpVMDelete, brokerPath),
		NewVmExecutor(broker.OpVMDiskList, brokerPath),
		NewVmExecutor(broker.OpVMDiskAdd, brokerPath),
		NewVmExecutor(broker.OpVMSnapshotList, brokerPath),
		NewVmExecutor(broker.OpVMSnapshotCreate, brokerPath),
		NewVmExecutor(broker.OpVMSnapshotRevert, brokerPath),
		NewVmExecutor(broker.OpVMSnapshotDelete, brokerPath),
		// Faz 13: log arama (agent-local, salt-okuma journald — ADR-0017).
		NewLogsExecutor(),
		// Faz 14: yedekleme ailesi (5 op — gerçek yürütme yalnız broker'da
		// --backup-enable ile; ADR-0018).
		NewBackupExecutor(broker.OpBackupRepoInit, brokerPath),
		NewBackupExecutor(broker.OpBackupRun, brokerPath),
		NewBackupExecutor(broker.OpBackupSnapshotList, brokerPath),
		NewBackupExecutor(broker.OpBackupVerify, brokerPath),
		NewBackupExecutor(broker.OpBackupRestore, brokerPath),
	)
}
