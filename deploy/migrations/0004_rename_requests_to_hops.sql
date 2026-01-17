-- Rename help request tables to hop terminology.

ALTER TABLE IF EXISTS request_transactions RENAME TO hop_transactions;
ALTER TABLE IF EXISTS requests RENAME TO hops;

ALTER TABLE IF EXISTS hop_transactions RENAME COLUMN request_id TO hop_id;

ALTER INDEX IF EXISTS requests_org_status_created_at_idx RENAME TO hops_org_status_created_at_idx;
ALTER INDEX IF EXISTS requests_org_created_by_created_at_idx RENAME TO hops_org_created_by_created_at_idx;
ALTER INDEX IF EXISTS requests_org_accepted_by_created_at_idx RENAME TO hops_org_accepted_by_created_at_idx;
ALTER INDEX IF EXISTS request_transactions_request_id_uniq RENAME TO hop_transactions_hop_id_uniq;
ALTER INDEX IF EXISTS request_transactions_org_to_idx RENAME TO hop_transactions_org_to_idx;
ALTER INDEX IF EXISTS request_transactions_org_from_idx RENAME TO hop_transactions_org_from_idx;
