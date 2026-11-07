-- +goose Up
-- +goose StatementBegin
CREATE VIEW resource_references AS
SELECT resource_id FROM sessions WHERE resource_id IS NOT NULL
UNION ALL SELECT resource_id FROM task_instances WHERE resource_id IS NOT NULL
UNION ALL SELECT resource_id FROM node_executions WHERE resource_id IS NOT NULL
UNION ALL SELECT subscription_id FROM node_executions WHERE subscription_id IS NOT NULL
UNION ALL SELECT resource_id FROM population_members
UNION ALL SELECT resource_id FROM subscription_retries;

CREATE TRIGGER sessions_release_resource AFTER DELETE ON sessions BEGIN
    DELETE FROM resources WHERE id = OLD.resource_id AND NOT EXISTS (SELECT 1 FROM resource_references WHERE resource_id = OLD.resource_id);
END;

CREATE TRIGGER sessions_release_replaced_resource AFTER UPDATE OF resource_id ON sessions BEGIN
    DELETE FROM resources WHERE id = OLD.resource_id AND NOT EXISTS (SELECT 1 FROM resource_references WHERE resource_id = OLD.resource_id);
END;

CREATE TRIGGER task_instances_release_resource AFTER DELETE ON task_instances BEGIN
    DELETE FROM resources WHERE id = OLD.resource_id AND NOT EXISTS (SELECT 1 FROM resource_references WHERE resource_id = OLD.resource_id);
END;

CREATE TRIGGER task_instances_release_replaced_resource AFTER UPDATE OF resource_id ON task_instances BEGIN
    DELETE FROM resources WHERE id = OLD.resource_id AND NOT EXISTS (SELECT 1 FROM resource_references WHERE resource_id = OLD.resource_id);
END;

CREATE TRIGGER node_executions_release_resource AFTER DELETE ON node_executions BEGIN
    DELETE FROM resources WHERE id = OLD.resource_id AND NOT EXISTS (SELECT 1 FROM resource_references WHERE resource_id = OLD.resource_id);
    DELETE FROM resources WHERE id = OLD.subscription_id AND NOT EXISTS (SELECT 1 FROM resource_references WHERE resource_id = OLD.subscription_id);
END;

CREATE TRIGGER node_executions_release_replaced_resource AFTER UPDATE OF resource_id, subscription_id ON node_executions BEGIN
    DELETE FROM resources WHERE id = OLD.resource_id AND NOT EXISTS (SELECT 1 FROM resource_references WHERE resource_id = OLD.resource_id);
    DELETE FROM resources WHERE id = OLD.subscription_id AND NOT EXISTS (SELECT 1 FROM resource_references WHERE resource_id = OLD.subscription_id);
END;

CREATE TRIGGER population_members_release_resource AFTER DELETE ON population_members BEGIN
    DELETE FROM resources WHERE id = OLD.resource_id AND NOT EXISTS (SELECT 1 FROM resource_references WHERE resource_id = OLD.resource_id);
END;

CREATE TRIGGER population_members_release_replaced_resource AFTER UPDATE OF resource_id ON population_members BEGIN
    DELETE FROM resources WHERE id = OLD.resource_id AND NOT EXISTS (SELECT 1 FROM resource_references WHERE resource_id = OLD.resource_id);
END;

CREATE TRIGGER subscription_retries_release_resource AFTER DELETE ON subscription_retries BEGIN
    DELETE FROM resources WHERE id = OLD.resource_id AND NOT EXISTS (SELECT 1 FROM resource_references WHERE resource_id = OLD.resource_id);
END;

CREATE TRIGGER subscription_retries_release_replaced_resource AFTER UPDATE OF resource_id ON subscription_retries BEGIN
    DELETE FROM resources WHERE id = OLD.resource_id AND NOT EXISTS (SELECT 1 FROM resource_references WHERE resource_id = OLD.resource_id);
END;
-- +goose StatementEnd
