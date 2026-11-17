CREATE INDEX CONCURRENTLY IF NOT EXISTS agentharbor_bark_delivery_due ON agentharbor_bark_delivery (available_at) WHERE sent_at IS NULL;
