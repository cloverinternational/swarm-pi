if (!process.env.PI_SWARM_POSTGRES_URL) { console.error("Set PI_SWARM_POSTGRES_URL to run optional live Postgres/Absurd integration tests."); process.exit(2); }
console.log("Optional integration path enabled; add environment-specific migrations/fixtures before running.");
