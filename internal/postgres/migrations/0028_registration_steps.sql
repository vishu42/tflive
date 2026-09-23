-- A registration's step is what its sync is doing right now, for the people
-- watching it, as a run's is (0026). It is not its status, and nothing
-- branches on it. It is written when a step starts and kept when the sync
-- ends, so a failed sync still says where it failed. '' is a registration
-- whose sync has not started a step.
--
-- The list must stay equal to domain.AllTemplateRegistrationSteps.
alter table template_registrations
	add column step text not null default ''
	constraint template_registrations_step_check check (
		step in (
			'',
			'syncing'
		)
	);
