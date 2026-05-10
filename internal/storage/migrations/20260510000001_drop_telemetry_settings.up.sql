-- Removes the three telemetry rows from existing settings tables.
-- Telemetry was a Phase-A scaffold (LEARNINGS §7.3) that never shipped
-- a transport; the package, UI, and accessors have been deleted, so
-- the rows are dead data on installed copies.
--
-- Idempotent — DELETE on missing rows is a no-op. Safe on fresh installs
-- where the rows were never written.
DELETE FROM settings WHERE key IN (
    'telemetry.opt_in',
    'telemetry.client_id',
    'telemetry.decided'
);
