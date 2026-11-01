-- 0007_task_execution_target geri alınması.
DROP INDEX IF EXISTS tasks_node_queued_idx;
ALTER TABLE tasks DROP COLUMN IF EXISTS execution_target;
