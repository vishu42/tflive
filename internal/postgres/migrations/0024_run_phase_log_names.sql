-- Init and workspace selection run in both a run's plan phase and its apply
-- phase, so their logs are named for the phase that ran them: plan-init,
-- plan-workspace, apply-init, apply-workspace (logsink.FileNameForTerraformCommand).
--
-- Existing init and workspace logs were all written by a plan phase, since an
-- apply phase used to log its setup into its apply log. Their object keys are
-- unchanged, so they stay readable under the new names.

alter table template_run_logs drop constraint template_run_logs_phase_check;

update template_run_logs set phase = 'plan-init' where phase = 'init';
update template_run_logs set phase = 'plan-workspace' where phase = 'workspace';

alter table template_run_logs add constraint template_run_logs_phase_check check (
	phase in (
		'clone',
		'plan-init',
		'plan-workspace',
		'plan',
		'apply-init',
		'apply-workspace',
		'apply',
		'destroy'
	)
);
