alter table alerts add column assigned_analyst_id uuid references users(id);
