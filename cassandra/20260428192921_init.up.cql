-- Keyspace for webhook subscriptions
CREATE KEYSPACE IF NOT EXISTS webhooks
WITH replication = {'class': 'NetworkTopologyStrategy', 'dc1': 3}
 AND durable_writes = true;

-- Table for subscriptions
-- Partition key: (source, event_type) - optimized for lookup by source and event type
-- Clustering key: subscription_id - allows multiple subscriptions per (source, event_type)
CREATE TABLE IF NOT EXISTS webhooks.subscriptions (
    source text,
    event_type text,
    subscription_id uuid,
    destination_url text,
    http_method text,
    headers frozen<map<text, text>>,
    enabled boolean,
    created_at timestamp,
    PRIMARY KEY ((source, event_type), subscription_id)
) WITH CLUSTERING ORDER BY (subscription_id ASC)
  AND default_time_to_live = 0
  AND gc_grace_seconds = 86400;