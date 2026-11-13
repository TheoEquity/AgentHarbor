DELETE FROM agentharbor_event WHERE event_type = 'memory_read';

ALTER TABLE agentharbor_event
    DROP CONSTRAINT agentharbor_event_event_type_check,
    DROP CONSTRAINT agentharbor_event_evidence_check,
    ADD CONSTRAINT agentharbor_event_event_type_check
        CHECK (event_type IN ('prompt_started', 'turn_stopped', 'interrupted')),
    ADD CONSTRAINT agentharbor_event_evidence_check
        CHECK (char_length(evidence) <= 512);
