ALTER TABLE agentharbor_event
    DROP CONSTRAINT agentharbor_event_event_type_check,
    DROP CONSTRAINT agentharbor_event_evidence_check,
    ADD CONSTRAINT agentharbor_event_event_type_check
        CHECK (event_type IN ('prompt_started', 'turn_stopped', 'interrupted', 'memory_read')),
    ADD CONSTRAINT agentharbor_event_evidence_check
        CHECK (CASE WHEN event_type = 'memory_read'
            THEN octet_length(evidence) <= 8192
            ELSE char_length(evidence) <= 512 END);
