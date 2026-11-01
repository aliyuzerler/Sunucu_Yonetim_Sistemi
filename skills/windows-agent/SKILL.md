# Skill: Windows Agent

Use Windows-native APIs for Service Control Manager, Event Log, firewall and host facts. Agent communicates outbound via mTLS. Use a named-pipe broker with restrictive ACL if privilege separation is required. Do not expose arbitrary PowerShell. If fixed PowerShell scripts are unavoidable, sign/version them, use structured parameters, validate all values and audit. Test Windows Server versions in disposable VMs; do not infer Windows client compatibility from server tests.
