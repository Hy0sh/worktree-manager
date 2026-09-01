import json, os, psycopg
from http.server import BaseHTTPRequestHandler, HTTPServer
class H(BaseHTTPRequestHandler):
    def do_GET(self):
        with psycopg.connect(os.environ["DATABASE_URL"]) as c:
            rows = c.execute("SELECT id, label, stock FROM widgets").fetchall()
        body = json.dumps({"count": len(rows), "widgets": [dict(zip(("id", "label", "stock"), r)) for r in rows]}).encode()
        self.send_response(200); self.send_header("Content-Type", "application/json"); self.end_headers(); self.wfile.write(body)
    def log_message(self, *a): pass
HTTPServer(("0.0.0.0", 8099), H).serve_forever()
