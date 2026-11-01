-- 0002_identity_rbac geri alınması: en bağımlı tablodan bağımsıza doğru.
DROP TABLE IF EXISTS sessions;
DROP TABLE IF EXISTS admin_bootstrap_tokens;
DROP TABLE IF EXISTS user_role_bindings;
DROP TABLE IF EXISTS role_permissions;
DROP TABLE IF EXISTS permissions;
DROP TABLE IF EXISTS roles;
DROP TABLE IF EXISTS users;
