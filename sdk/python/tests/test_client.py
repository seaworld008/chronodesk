from __future__ import annotations

import base64
import json
import threading
import unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib import parse

from chronodesk import Audience, ChronoDeskClient, ClientCredentials


class _ContractHandler(BaseHTTPRequestHandler):
    base_url = ""

    def do_POST(self) -> None:
        if self.path != "/oauth/token":
            self.send_error(404)
            return
        expected = "Basic " + base64.b64encode(b"client:secret-value").decode()
        if self.headers.get("Authorization") != expected:
            self.send_error(401)
            return
        length = int(self.headers.get("Content-Length", "0"))
        form = parse.parse_qs(self.rfile.read(length).decode())
        if form.get("project_key") != ["OPS"] or form.get("resource") != [
            self.base_url + "/api/v2"
        ]:
            self.send_error(400)
            return
        self._json(
            {
                "access_token": "api-token",
                "token_type": "Bearer",
                "expires_in": 600,
                "scope": "tickets:read",
                "resource": self.base_url + "/api/v2",
                "project_key": "OPS",
            }
        )

    def do_GET(self) -> None:
        if self.headers.get("Authorization") != "Bearer api-token":
            self.send_error(401)
            return
        parsed = parse.urlsplit(self.path)
        if parsed.path == "/api/v2/projects/OPS/capabilities":
            self._json(
                {
                    "data": {
                        "api_version": "v2",
                        "openapi": "/openapi.yaml",
                        "asyncapi": "/asyncapi.yaml",
                        "mcp_endpoint": "/mcp",
                        "mcp_version": "2026-07-28",
                        "a2a_endpoint": "/a2a/v1",
                        "a2a_version": "1.0",
                        "agent_card": "/.well-known/agent-card.json",
                        "oauth_metadata": {
                            "api": "/.well-known/oauth-protected-resource/api/v2",
                            "mcp": "/.well-known/oauth-protected-resource/mcp",
                            "a2a": "/.well-known/oauth-protected-resource/a2a/v1",
                        },
                        "scopes_supported": ["tickets:read"],
                        "concurrency": {
                            "optimistic_version": True,
                            "ticket_leases": True,
                            "idempotency_keys": True,
                        },
                    },
                    "meta": {"request_id": "r1"},
                }
            )
            return
        if parsed.path == "/api/v2/projects/OPS/tickets":
            if parse.parse_qs(parsed.query) != {"limit": ["10"], "status": ["open"]}:
                self.send_error(400)
                return
            self._json(
                {
                    "data": [{"id": 42, "ticket_number": "OPS-42"}],
                    "meta": {"request_id": "r2"},
                }
            )
            return
        self.send_error(404)

    def log_message(self, _format: str, *args: object) -> None:
        del args

    def _json(self, payload: dict[str, object]) -> None:
        body = json.dumps(payload).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)


class ChronoDeskClientContractTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        cls.server = ThreadingHTTPServer(("127.0.0.1", 0), _ContractHandler)
        host, port = cls.server.server_address
        cls.base_url = f"http://{host}:{port}"
        _ContractHandler.base_url = cls.base_url
        cls.thread = threading.Thread(target=cls.server.serve_forever, daemon=True)
        cls.thread.start()

    @classmethod
    def tearDownClass(cls) -> None:
        cls.server.shutdown()
        cls.server.server_close()
        cls.thread.join(timeout=5)

    def test_oauth_project_and_audience_binding(self) -> None:
        client = ChronoDeskClient(self.base_url, "OPS")
        token = client.exchange_client_credentials(
            ClientCredentials(
                client_id="client",
                client_secret="secret-value",
                audience=Audience.API,
                scopes=("tickets:read",),
            )
        )
        self.assertEqual(token.project_key, "OPS")
        self.assertEqual(token.resource, self.base_url + "/api/v2")

    def test_capabilities_and_tickets_are_project_scoped(self) -> None:
        client = ChronoDeskClient(
            self.base_url,
            "OPS",
            access_token="api-token",
        )
        self.assertEqual(client.capabilities()["data"]["api_version"], "v2")
        tickets = client.list_tickets(limit=10, status="open")
        self.assertEqual(tickets["data"][0]["ticket_number"], "OPS-42")

    def test_missing_project_audience_and_token_fail_closed(self) -> None:
        with self.assertRaises(ValueError):
            ChronoDeskClient(self.base_url, "")
        client = ChronoDeskClient(self.base_url, "OPS")
        with self.assertRaises(TypeError):
            client.exchange_client_credentials(
                ClientCredentials(
                    client_id="client",
                    client_secret="secret",
                    audience="api",  # type: ignore[arg-type]
                )
            )
        with self.assertRaises(ValueError):
            client.capabilities()
        with self.assertRaises(ValueError):
            ChronoDeskClient("http://desk.example", "OPS")
        with self.assertRaises(ValueError):
            ChronoDeskClient("https://desk.example/base", "OPS")
        with self.assertRaises(ValueError):
            ChronoDeskClient("https://desk.example", "OPS", timeout=float("nan"))


if __name__ == "__main__":
    unittest.main()
