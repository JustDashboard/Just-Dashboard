"""The HTTP app and its recovery checker use the same database access path."""

import json
import os
import sqlite3
import sys
from http.server import BaseHTTPRequestHandler, HTTPServer


def read_record(path):
    with sqlite3.connect(f"file:{path}?mode=ro", uri=True) as database:
        schema = database.execute("PRAGMA user_version").fetchone()[0]
        value = database.execute("SELECT value FROM records WHERE id=1").fetchone()[0]
        return {"schema": schema, "canary": value}


if len(sys.argv) == 3 and sys.argv[1] == "verify":
    record = read_record(sys.argv[2])
    assert str(record["schema"]) == os.environ["JD_RESTORE_SCHEMA_VERSION"]
    print(f"schema-{record['schema']}:{record['canary']}")
else:
    class Handler(BaseHTTPRequestHandler):
        def do_GET(self):
            payload = json.dumps(read_record(os.environ["DATABASE_PATH"])).encode()
            self.send_response(200)
            self.send_header("Content-Length", str(len(payload)))
            self.end_headers()
            self.wfile.write(payload)

    HTTPServer(("0.0.0.0", 8080), Handler).serve_forever()
