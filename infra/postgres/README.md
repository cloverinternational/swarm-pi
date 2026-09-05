# Local Absurd/Postgres

Copy `.env.example` to `.env`, then run:

```sh
docker compose --env-file .env up -d
pg_isready -d "$ABSURD_DATABASE_URL"
```

The first database initialization applies the pinned Absurd 0.5.0-compatible
`absurd.sql` and creates queue `pi-swarm`. Existing Postgres deployments should
apply `absurd.sql` through their migration system and run `select
absurd.create_queue('pi-swarm');`; do not use SQLite. Compose init scripts only
run for a new volume. The application uses `ABSURD_DATABASE_URL` and
`ABSURD_QUEUE`.

Optional integration checks can use `docker compose ... up` and are intentionally
not part of ordinary CI.
