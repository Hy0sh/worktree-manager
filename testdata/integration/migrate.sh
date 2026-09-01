#!/bin/sh
# `docker compose run` replaces api's command, so the pip install baked into
# it never happens: the migration container starts bare and must install its
# own dependency.
set -e
pip install -q psycopg[binary]
python3 - <<'PY'
import os, psycopg
with psycopg.connect(os.environ["DATABASE_URL"]) as c:
    c.execute("CREATE TABLE IF NOT EXISTS widgets(id serial primary key, label text, stock int)")
    c.execute("INSERT INTO widgets(label, stock) SELECT 'from-migration', 7 WHERE NOT EXISTS (SELECT 1 FROM widgets)")
PY
