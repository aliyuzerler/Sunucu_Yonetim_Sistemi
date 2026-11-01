# Skill: Linux Agent and System Adapters

Separate observation from mutation. Detect distro/version/init/package manager before claiming a capability. Implement systemd, package, network, firewall, filesystem and process operations behind interfaces. Use native APIs where possible; if invoking a binary, use a fixed executable path and structured allowlisted arguments—never shell concatenation. Use a privileged local broker, strict IPC permissions, peer identity checks and per-operation audit. Test on Ubuntu 24.04 and Debian 13 at minimum, with mocks and disposable VM integration tests.
