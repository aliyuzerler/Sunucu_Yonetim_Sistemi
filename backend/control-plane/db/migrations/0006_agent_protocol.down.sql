-- 0006_agent_protocol geri alınması.
DROP TABLE IF EXISTS node_certificates;
DROP TABLE IF EXISTS enrollment_tokens;
ALTER TABLE nodes DROP COLUMN IF EXISTS protocol_version;
