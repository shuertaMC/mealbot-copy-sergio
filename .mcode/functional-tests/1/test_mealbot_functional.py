"""Functional tests for the real Mealbot binary, PostgreSQL, and HTTP API."""

from __future__ import annotations

import base64
import json
import os
import socket
import socketserver
import ssl
import subprocess
import tempfile
import threading
import time
from contextlib import contextmanager
from dataclasses import dataclass
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from typing import Any
from urllib.parse import urlencode

import pytest
import requests


REPO = Path(__file__).resolve().parents[3]
BINARY = Path(
    os.environ.get(
        "MEALBOT_BINARY",
        str(Path(os.environ["MCODE_DIR"]) / "lifecycle/target/bin/mealbot"),
    )
)
DATABASE_URL = os.environ.get(
    "DATABASE_URL",
    "postgresql://morphagent@localhost/mealbot_test?host=/var/run/postgresql",
)
EVIDENCE_DIR = Path(os.environ.get("FT_EVIDENCE_DIR", Path(tempfile.gettempdir()) / "mealbot-ft-evidence"))
ALLOWED_ORIGIN = "https://allowed.example"
AUDIENCE = "mealbot-functional"


def _free_port() -> int:
    with socket.socket() as listener:
        listener.bind(("127.0.0.1", 0))
        return int(listener.getsockname()[1])


def _b64url(value: bytes) -> str:
    return base64.urlsafe_b64encode(value).rstrip(b"=").decode("ascii")


def _integer_bytes(value: int) -> bytes:
    return value.to_bytes((value.bit_length() + 7) // 8, "big")


def _run(
    command: list[str],
    *,
    env: dict[str, str] | None = None,
    input_text: str | None = None,
    timeout: float = 30,
) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        command,
        cwd=REPO,
        env=env,
        input=input_text,
        text=True,
        capture_output=True,
        timeout=timeout,
        check=False,
    )


def _psql(sql: str, database_url: str = DATABASE_URL) -> str:
    result = _run(
        ["psql", database_url, "-v", "ON_ERROR_STOP=1", "-At"],
        input_text=sql,
    )
    assert result.returncode == 0, result.stderr
    return result.stdout


def _record_http(
    name: str,
    response: requests.Response,
    expected_status: int,
    *,
    endpoint: str,
    method: str,
    request_body: str = "",
) -> None:
    EVIDENCE_DIR.mkdir(parents=True, exist_ok=True)
    evidence = {
        "name": name,
        "protocol": "rest",
        "endpoint": endpoint,
        "method": method,
        "request_body": request_body,
        "expected_status": expected_status,
        "actual_status": response.status_code,
        "actual_output": response.text,
        "duration_ms": response.elapsed.total_seconds() * 1000,
    }
    (EVIDENCE_DIR / f"{name}.json").write_text(json.dumps(evidence), encoding="utf-8")


def _record_cli(
    name: str,
    result: subprocess.CompletedProcess[str],
    expected_exit_code: int,
    *,
    command: str,
    args: str,
    started_at: float,
) -> None:
    EVIDENCE_DIR.mkdir(parents=True, exist_ok=True)
    evidence = {
        "name": name,
        "protocol": "cli",
        "command": command,
        "args": args,
        "expected_exit_code": expected_exit_code,
        "actual_exit_code": result.returncode,
        "actual_output": result.stdout + result.stderr,
        "duration_ms": (time.monotonic() - started_at) * 1000,
    }
    (EVIDENCE_DIR / f"{name}.json").write_text(json.dumps(evidence), encoding="utf-8")


class _JWKSHandler(BaseHTTPRequestHandler):
    document: bytes = b""

    def do_GET(self) -> None:  # noqa: N802 - BaseHTTPRequestHandler contract
        if self.path != "/.well-known/jwks.json":
            self.send_error(404)
            return
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(self.document)))
        self.end_headers()
        self.wfile.write(self.document)

    def log_message(self, _format: str, *_args: Any) -> None:
        return


class _MailgunProxyHandler(socketserver.BaseRequestHandler):
    def handle(self) -> None:
        connection = self.request
        connect_request = self._read_headers(connection)
        assert connect_request.startswith(b"CONNECT api.mailgun.net:443 ")
        connection.sendall(b"HTTP/1.1 200 Connection Established\r\n\r\n")
        with self.server.ssl_context.wrap_socket(connection, server_side=True) as tls:  # type: ignore[attr-defined]
            request_headers = self._read_headers(tls)
            header_text = request_headers.decode("iso-8859-1")
            content_length = 0
            for line in header_text.split("\r\n"):
                if line.lower().startswith("content-length:"):
                    content_length = int(line.split(":", 1)[1].strip())
            body = bytearray()
            while len(body) < content_length:
                part = tls.recv(content_length - len(body))
                if not part:
                    break
                body.extend(part)
            self.server.requests.append((request_headers, bytes(body)))  # type: ignore[attr-defined]
            status = self.server.response_status  # type: ignore[attr-defined]
            if status == 200:
                response_body = b'{"id":"functional-local","message":"Queued. Thank you."}'
                status_line = b"HTTP/1.1 200 OK\r\n"
            else:
                response_body = b'{"message":"functional delivery failure"}'
                status_line = b"HTTP/1.1 500 Internal Server Error\r\n"
            tls.sendall(
                status_line
                + b"Content-Type: application/json\r\n"
                + f"Content-Length: {len(response_body)}\r\n".encode()
                + b"Connection: close\r\n\r\n"
                + response_body
            )

    @staticmethod
    def _read_headers(connection: socket.socket) -> bytes:
        data = bytearray()
        while b"\r\n\r\n" not in data:
            part = connection.recv(1)
            if not part:
                break
            data.extend(part)
        return bytes(data)


class _ThreadedTCPServer(socketserver.ThreadingMixIn, socketserver.TCPServer):
    allow_reuse_address = True
    daemon_threads = True


@contextmanager
def _local_mailgun(response_status: int):
    directory = EVIDENCE_DIR / f"mailgun-{response_status}"
    directory.mkdir(parents=True, exist_ok=True)
    ca_key = directory / "ca.key"
    ca_cert = directory / "ca.pem"
    server_key = directory / "server.key"
    server_csr = directory / "server.csr"
    server_cert = directory / "server.pem"
    extensions = directory / "extensions.cnf"
    extensions.write_text("subjectAltName=DNS:api.mailgun.net\n", encoding="utf-8")
    commands = [
        ["openssl", "req", "-x509", "-newkey", "rsa:2048", "-nodes", "-days", "1", "-keyout", str(ca_key), "-out", str(ca_cert), "-subj", "/CN=Mealbot Functional CA"],
        ["openssl", "req", "-new", "-newkey", "rsa:2048", "-nodes", "-keyout", str(server_key), "-out", str(server_csr), "-subj", "/CN=api.mailgun.net"],
        ["openssl", "x509", "-req", "-in", str(server_csr), "-CA", str(ca_cert), "-CAkey", str(ca_key), "-CAcreateserial", "-days", "1", "-sha256", "-extfile", str(extensions), "-out", str(server_cert)],
    ]
    for command in commands:
        result = _run(command)
        assert result.returncode == 0, result.stderr
    context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
    context.load_cert_chain(server_cert, server_key)
    server = _ThreadedTCPServer(("127.0.0.1", 0), _MailgunProxyHandler)
    server.ssl_context = context  # type: ignore[attr-defined]
    server.response_status = response_status  # type: ignore[attr-defined]
    server.requests = []  # type: ignore[attr-defined]
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    try:
        yield server, ca_cert
    finally:
        server.shutdown()
        server.server_close()
        thread.join(timeout=5)


@dataclass
class Runtime:
    base_url: str
    token: str
    env: dict[str, str]

    @property
    def auth(self) -> dict[str, str]:
        return {"Authorization": f"Bearer {self.token}"}


def _signed_token(private_key: Path, issuer: str) -> str:
    now = int(time.time())
    header = _b64url(json.dumps({"alg": "RS256", "kid": "functional-key", "typ": "JWT"}, separators=(",", ":")).encode())
    payload = _b64url(
        json.dumps(
            {"iss": issuer, "aud": AUDIENCE, "sub": "functional-tester", "iat": now, "exp": now + 3600},
            separators=(",", ":"),
        ).encode()
    )
    signing_input = f"{header}.{payload}".encode("ascii")
    signed = subprocess.run(
        ["openssl", "dgst", "-sha256", "-sign", str(private_key)],
        cwd=REPO,
        input=signing_input,
        capture_output=True,
        timeout=10,
        check=False,
    )
    assert signed.returncode == 0, signed.stderr.decode(errors="replace")
    signature = signed.stdout
    return f"{header}.{payload}.{_b64url(signature)}"


@pytest.fixture(scope="session")
def runtime() -> Runtime:
    assert BINARY.is_file(), f"Mealbot binary not found: {BINARY}"
    EVIDENCE_DIR.mkdir(parents=True, exist_ok=True)
    for old in EVIDENCE_DIR.glob("*.json"):
        old.unlink()

    reset = _run(["psql", DATABASE_URL, "-v", "ON_ERROR_STOP=1", "-f", str(REPO / "internal/store/postgres/testdata/reset.sql")])
    assert reset.returncode == 0, reset.stderr

    base_env = os.environ.copy()
    base_env.update(
        {
            "APP_ENV": "development",
            "DATABASE_URL": DATABASE_URL,
            "STATIC_DIR": str(REPO / "static"),
            "AUTH0_AUDIENCE": AUDIENCE,
            "CORS_ALLOWED_ORIGINS": ALLOWED_ORIGIN,
            "REQUEST_TIMEOUT": "5s",
            "JWKS_TIMEOUT": "2s",
            "MAIL_TIMEOUT": "2s",
        }
    )
    migrated = _run([str(BINARY), "migrate"], env=base_env)
    assert migrated.returncode == 0, migrated.stderr

    private_key = EVIDENCE_DIR / "functional-private.pem"
    generated = _run(["openssl", "genpkey", "-algorithm", "RSA", "-pkeyopt", "rsa_keygen_bits:2048", "-out", str(private_key)])
    assert generated.returncode == 0, generated.stderr
    modulus_result = _run(["openssl", "rsa", "-in", str(private_key), "-noout", "-modulus"])
    assert modulus_result.returncode == 0, modulus_result.stderr
    modulus = bytes.fromhex(modulus_result.stdout.strip().split("=", 1)[1])
    jwks = {
        "keys": [
            {
                "kty": "RSA",
                "kid": "functional-key",
                "use": "sig",
                "alg": "RS256",
                "n": _b64url(modulus),
                "e": _b64url(_integer_bytes(65537)),
            }
        ]
    }
    _JWKSHandler.document = json.dumps(jwks, separators=(",", ":")).encode()
    jwks_server = ThreadingHTTPServer(("127.0.0.1", _free_port()), _JWKSHandler)
    jwks_thread = threading.Thread(target=jwks_server.serve_forever, daemon=True)
    jwks_thread.start()
    issuer = f"http://127.0.0.1:{jwks_server.server_port}/"

    port = _free_port()
    env = base_env.copy()
    env.update(
        {
            "PORT": str(port),
            "AUTH0_ISSUER": issuer,
            "AUTH0_JWKS_URL": issuer + ".well-known/jwks.json",
        }
    )
    stdout_path = EVIDENCE_DIR / "mealbot.stdout.log"
    stderr_path = EVIDENCE_DIR / "mealbot.stderr.log"
    with stdout_path.open("wb") as stdout, stderr_path.open("wb") as stderr:
        process = subprocess.Popen([str(BINARY)], cwd=REPO, env=env, stdout=stdout, stderr=stderr)
        base_url = f"http://127.0.0.1:{port}"
        for _ in range(100):
            if process.poll() is not None:
                break
            try:
                response = requests.get(f"{base_url}/privacy.html", timeout=0.5)
                if response.status_code == 200:
                    break
            except requests.RequestException:
                pass
            time.sleep(0.05)
        else:
            process.terminate()
            raise AssertionError("Mealbot server did not become ready")
        if process.poll() is not None:
            raise AssertionError(f"Mealbot exited during startup: {stderr_path.read_text(errors='replace')}")

        try:
            yield Runtime(base_url=base_url, token=_signed_token(private_key, issuer), env=env)
        finally:
            process.terminate()
            try:
                process.wait(timeout=5)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait(timeout=5)
            jwks_server.shutdown()
            jwks_server.server_close()
            jwks_thread.join(timeout=5)
            private_key.unlink(missing_ok=True)


@pytest.fixture(autouse=True)
def health_check(runtime: Runtime) -> None:
    response = requests.get(f"{runtime.base_url}/privacy.html", timeout=5)
    assert response.status_code == 200


def _create_org(runtime: Runtime, name: str, admin: str) -> requests.Response:
    return requests.post(
        f"{runtime.base_url}/org",
        params={"admin": admin},
        json={"org": name},
        headers=runtime.auth,
        timeout=10,
    )


def _import_members(runtime: Runtime, organization: str, contents: str) -> requests.Response:
    return requests.post(
        f"{runtime.base_url}/members",
        params={"org": organization},
        files={"members": ("members.csv", contents, "text/csv")},
        headers=runtime.auth,
        timeout=10,
    )


def _add_round(runtime: Runtime, organization: str, timestamp: str) -> requests.Response:
    return requests.post(
        f"{runtime.base_url}/round",
        params={"org": organization, "round": timestamp},
        headers=runtime.auth,
        timeout=10,
    )


class TestPublicAndMiddleware:
    def test_public_privacy(self, runtime: Runtime) -> None:
        response = requests.get(f"{runtime.base_url}/privacy.html", timeout=10)
        _record_http("test_public_privacy", response, 200, endpoint="/privacy.html", method="GET")
        assert response.status_code == 200
        assert "<title>Privacy Policy</title>" in response.text

    def test_public_sample_csv(self, runtime: Runtime) -> None:
        response = requests.get(f"{runtime.base_url}/sample.csv", timeout=10)
        _record_http("test_public_sample_csv", response, 200, endpoint="/sample.csv", method="GET")
        assert response.status_code == 200
        assert response.text.startswith("Name,Email,College,Year\n")
        assert "johnamadeo.daniswara@yale.edu" in response.text

    def test_auth_required(self, runtime: Runtime) -> None:
        response = requests.get(f"{runtime.base_url}/orgs", params={"admin": "nobody@example.test"}, timeout=10)
        _record_http("test_auth_required", response, 401, endpoint="/orgs", method="GET")
        assert response.status_code == 401
        assert response.json() == {"error": "invalid access token"}

    def test_cors_preflight_without_auth(self, runtime: Runtime) -> None:
        response = requests.options(
            f"{runtime.base_url}/orgs",
            headers={
                "Origin": ALLOWED_ORIGIN,
                "Access-Control-Request-Method": "GET",
                "Access-Control-Request-Headers": "Authorization, Content-Type",
            },
            timeout=10,
        )
        _record_http("test_cors_preflight_without_auth", response, 204, endpoint="/orgs", method="OPTIONS")
        assert response.status_code == 204
        assert response.headers["Access-Control-Allow-Origin"] == ALLOWED_ORIGIN

    def test_cors_forbidden_before_auth(self, runtime: Runtime) -> None:
        response = requests.get(
            f"{runtime.base_url}/orgs",
            params={"admin": "nobody@example.test"},
            headers={"Origin": "https://forbidden.example"},
            timeout=10,
        )
        _record_http("test_cors_forbidden_before_auth", response, 403, endpoint="/orgs", method="GET")
        assert response.status_code == 403
        assert response.json() == {"error": "origin not allowed"}


class TestOrganizations:
    def test_create_then_list_organization(self, runtime: Runtime) -> None:
        organization = "ftrun_org_flow"
        admin = "org-flow@example.test"
        created = _create_org(runtime, organization, admin)
        assert created.status_code == 201
        response = requests.get(
            f"{runtime.base_url}/orgs",
            params={"admin": admin},
            headers=runtime.auth,
            timeout=10,
        )
        _record_http(
            "test_create_then_list_organization",
            response,
            200,
            endpoint="/orgs",
            method="GET",
            request_body=json.dumps({"org": organization}),
        )
        assert response.status_code == 200
        assert response.json() == {"orgs": [organization]}

    def test_org_malformed_input(self, runtime: Runtime) -> None:
        response = requests.post(
            f"{runtime.base_url}/org",
            params={"admin": "malformed@example.test"},
            data="{",
            headers={**runtime.auth, "Content-Type": "application/json"},
            timeout=10,
        )
        _record_http(
            "test_org_malformed_input",
            response,
            400,
            endpoint="/org",
            method="POST",
            request_body="{",
        )
        assert response.status_code == 400
        assert response.json() == {"error": "invalid request"}

    def test_method_not_allowed(self, runtime: Runtime) -> None:
        response = requests.get(
            f"{runtime.base_url}/org",
            params={"admin": "method@example.test"},
            headers=runtime.auth,
            timeout=10,
        )
        _record_http("test_method_not_allowed", response, 405, endpoint="/org", method="GET")
        assert response.status_code == 405
        assert response.headers["Allow"] == "POST"


class TestMembers:
    def test_member_import_lifecycle(self, runtime: Runtime) -> None:
        organization = "ftrun_members_lifecycle"
        assert _create_org(runtime, organization, "members@example.test").status_code == 201
        initial = " Name , EMAIL , Team \nAlice,a-lifecycle@example.test,red\nBob,b-lifecycle@example.test,blue\nCara,c-lifecycle@example.test,green\n"
        imported = _import_members(runtime, organization, initial)
        assert imported.status_code == 201
        reduced = "name,email,team\nAlice Updated,a-lifecycle@example.test,red\nCara,c-lifecycle@example.test,green\n"
        assert _import_members(runtime, organization, reduced).status_code == 201
        middle = requests.get(
            f"{runtime.base_url}/members", params={"org": organization}, headers=runtime.auth, timeout=10
        )
        assert [member["email"] for member in middle.json()["members"]] == [
            "a-lifecycle@example.test",
            "c-lifecycle@example.test",
        ]
        reactivated = _import_members(runtime, organization, initial)
        assert reactivated.status_code == 201
        response = requests.get(
            f"{runtime.base_url}/members", params={"org": organization}, headers=runtime.auth, timeout=10
        )
        _record_http(
            "test_member_import_lifecycle",
            response,
            200,
            endpoint="/members",
            method="GET",
            request_body=initial,
        )
        assert response.status_code == 200
        assert len(response.json()["members"]) == 3
        assert response.json()["traits"] == ["team"]

    def test_member_import_invalid_csv(self, runtime: Runtime) -> None:
        organization = "ftrun_invalid_csv"
        assert _create_org(runtime, organization, "invalid-csv@example.test").status_code == 201
        invalid = "name,email,email\nAlice,a@example.test,a@example.test\n"
        response = _import_members(runtime, organization, invalid)
        _record_http(
            "test_member_import_invalid_csv",
            response,
            400,
            endpoint="/members",
            method="POST",
            request_body=invalid,
        )
        assert response.status_code == 400
        assert response.json() == {"error": "invalid request"}

    def test_crossmatch_trait(self, runtime: Runtime) -> None:
        organization = "ftrun_crossmatch"
        assert _create_org(runtime, organization, "crossmatch@example.test").status_code == 201
        assert _import_members(
            runtime,
            organization,
            "name,email,team\nAlice,a-cross@example.test,red\nBob,b-cross@example.test,blue\n",
        ).status_code == 201
        updated = requests.post(
            f"{runtime.base_url}/crossmatchtrait",
            params={"org": organization},
            json={"trait": "team"},
            headers=runtime.auth,
            timeout=10,
        )
        assert updated.status_code == 201
        response = requests.get(
            f"{runtime.base_url}/members", params={"org": organization}, headers=runtime.auth, timeout=10
        )
        _record_http(
            "test_crossmatch_trait",
            response,
            200,
            endpoint="/members",
            method="GET",
            request_body=json.dumps({"trait": "team"}),
        )
        assert response.status_code == 200
        assert response.json()["crossMatchTrait"] == "team"

    def test_member_reconcile_rollback(self, runtime: Runtime) -> None:
        organization = "ftrun_member_rollback"
        assert _create_org(runtime, organization, "rollback@example.test").status_code == 201
        initial = "name,email,team\nAlice,a-rollback@example.test,red\nBob,b-rollback@example.test,blue\n"
        assert _import_members(runtime, organization, initial).status_code == 201
        _psql(
            """
            CREATE FUNCTION ftrun_fail_member_reconcile() RETURNS trigger LANGUAGE plpgsql AS $$
            BEGIN
                IF NEW.email = 'b-rollback@example.test' THEN
                    RAISE EXCEPTION 'functional injected reconciliation failure';
                END IF;
                RETURN NEW;
            END $$;
            CREATE TRIGGER ftrun_fail_member_reconcile BEFORE INSERT OR UPDATE ON members
            FOR EACH ROW EXECUTE FUNCTION ftrun_fail_member_reconcile();
            """
        )
        attempted = "name,email,team\nAlice Changed,a-rollback@example.test,red\nBob,b-rollback@example.test,blue\n"
        try:
            response = _import_members(runtime, organization, attempted)
            _record_http(
                "test_member_reconcile_rollback",
                response,
                500,
                endpoint="/members",
                method="POST",
                request_body=attempted,
            )
            assert response.status_code == 500
            assert response.json() == {"error": "internal server error"}
        finally:
            _psql(
                "DROP TRIGGER IF EXISTS ftrun_fail_member_reconcile ON members; "
                "DROP FUNCTION IF EXISTS ftrun_fail_member_reconcile();"
            )
        after = requests.get(
            f"{runtime.base_url}/members", params={"org": organization}, headers=runtime.auth, timeout=10
        )
        names = {member["email"]: member["name"] for member in after.json()["members"]}
        assert names["a-rollback@example.test"] == "Alice"
        assert names["b-rollback@example.test"] == "Bob"


class TestRoundsAndHistory:
    def test_round_add_reschedule_delete(self, runtime: Runtime) -> None:
        organization = "ftrun_round_lifecycle"
        assert _create_org(runtime, organization, "rounds@example.test").status_code == 201
        assert _add_round(runtime, organization, "2030-01-01T10:00:00Z").status_code == 201
        rescheduled = requests.post(
            f"{runtime.base_url}/round",
            params={"org": organization, "round": "2030-01-02T11:00:00Z", "roundId": "0"},
            headers=runtime.auth,
            timeout=10,
        )
        assert rescheduled.status_code == 201
        listed = requests.get(
            f"{runtime.base_url}/rounds", params={"org": organization}, headers=runtime.auth, timeout=10
        )
        assert listed.json() == {"rounds": ["2030-01-02 11:00:00Z"]}
        removed = requests.delete(
            f"{runtime.base_url}/round",
            params={"org": organization, "roundId": "0"},
            headers=runtime.auth,
            timeout=10,
        )
        assert removed.status_code == 201
        response = requests.get(
            f"{runtime.base_url}/rounds", params={"org": organization}, headers=runtime.auth, timeout=10
        )
        _record_http(
            "test_round_add_reschedule_delete",
            response,
            200,
            endpoint="/rounds",
            method="GET",
            request_body=urlencode({"org": organization}),
        )
        assert response.status_code == 200
        assert response.json() == {"rounds": []}

    def test_empty_completed_round_history(self, runtime: Runtime) -> None:
        organization = "ftrun_empty_round_history"
        assert _create_org(runtime, organization, "empty-history@example.test").status_code == 201
        assert _add_round(runtime, organization, "2026-01-01T00:00:00Z").status_code == 201
        env = runtime.env.copy()
        env.update(
            {
                "MAILGUN_SMTP_LOGIN": "bot@example.test",
                "MAILGUN_DOMAIN": "example.test",
                "MAILGUN_API_KEY": "functional-local-only",
            }
        )
        paired = _run([str(BINARY), "pair"], env=env)
        assert paired.returncode == 0, paired.stderr
        response = requests.get(
            f"{runtime.base_url}/pairs", params={"org": organization}, headers=runtime.auth, timeout=10
        )
        _record_http(
            "test_empty_completed_round_history",
            response,
            200,
            endpoint="/pairs",
            method="GET",
        )
        assert response.status_code == 200
        assert response.json() == {"roundPairs": [[]]}

    def test_pair_and_triple_history_includes_inactive(self, runtime: Runtime) -> None:
        organization = "ftrun_pair_triple_history"
        assert _create_org(runtime, organization, "history@example.test").status_code == 201
        all_members = "name,email,team\nAlice,a-history@example.test,red\nBob,b-history@example.test,blue\nCara,c-history@example.test,green\n"
        assert _import_members(runtime, organization, all_members).status_code == 201
        for day in (1, 2, 3):
            assert _add_round(runtime, organization, f"2031-01-0{day}T00:00:00Z").status_code == 201
        _psql(
            """
            UPDATE rounds SET done=true WHERE organization='ftrun_pair_triple_history';
            INSERT INTO pairs (organization, round, id1, id2, extra_id) VALUES
              ('ftrun_pair_triple_history',1,'a-history@example.test','b-history@example.test',NULL),
              ('ftrun_pair_triple_history',2,'a-history@example.test','b-history@example.test','c-history@example.test');
            """
        )
        active_only = "name,email,team\nAlice,a-history@example.test,red\nBob,b-history@example.test,blue\n"
        assert _import_members(runtime, organization, active_only).status_code == 201
        response = requests.get(
            f"{runtime.base_url}/pairs", params={"org": organization}, headers=runtime.auth, timeout=10
        )
        _record_http(
            "test_pair_and_triple_history_includes_inactive",
            response,
            200,
            endpoint="/pairs",
            method="GET",
        )
        assert response.status_code == 200
        round_pairs = response.json()["roundPairs"]
        assert len(round_pairs) == 3
        assert round_pairs[0] == []
        assert round_pairs[1][0]["member2"]["email"] == "b-history@example.test"
        assert round_pairs[2][0]["extraMember"]["email"] == "c-history@example.test"
        assert round_pairs[2][0]["extraMember"]["name"] == "Cara"


class TestCLI:
    def test_cli_pair_empty_population(self, runtime: Runtime) -> None:
        organization = "ftrun_cli_pair_empty"
        assert _create_org(runtime, organization, "cli-pair@example.test").status_code == 201
        assert _add_round(runtime, organization, "2026-02-01T00:00:00Z").status_code == 201
        env = runtime.env.copy()
        env.update(
            {
                "MAILGUN_SMTP_LOGIN": "bot@example.test",
                "MAILGUN_DOMAIN": "example.test",
                "MAILGUN_API_KEY": "functional-local-only",
            }
        )
        started = time.monotonic()
        result = _run([str(BINARY), "pair"], env=env)
        _record_cli(
            "test_cli_pair_empty_population",
            result,
            0,
            command="mealbot",
            args="pair",
            started_at=started,
        )
        assert result.returncode == 0, result.stderr
        done = _psql(
            "SELECT done FROM rounds WHERE organization='ftrun_cli_pair_empty' AND id=0;"
        ).strip()
        assert done == "t"

    def test_cli_pair_odd_population_forms_triple(self, runtime: Runtime) -> None:
        organization = "ftrun_cli_pair_triple"
        assert _create_org(runtime, organization, "cli-triple@example.test").status_code == 201
        members = "name,email,team\nAlice,a-cli-triple@example.test,red\nBob,b-cli-triple@example.test,blue\nCara,c-cli-triple@example.test,green\n"
        assert _import_members(runtime, organization, members).status_code == 201
        assert _add_round(runtime, organization, "2026-03-01T00:00:00Z").status_code == 201
        with _local_mailgun(200) as (proxy, ca_cert):
            env = runtime.env.copy()
            env.update(
                {
                    "MAILGUN_SMTP_LOGIN": "bot@example.test",
                    "MAILGUN_DOMAIN": "example.test",
                    "MAILGUN_API_KEY": "functional-local-only",
                    "HTTPS_PROXY": f"http://127.0.0.1:{proxy.server_address[1]}",
                    "NO_PROXY": "",
                    "no_proxy": "",
                    "SSL_CERT_FILE": str(ca_cert),
                }
            )
            started = time.monotonic()
            result = _run([str(BINARY), "pair"], env=env)
            _record_cli(
                "test_cli_pair_odd_population_forms_triple",
                result,
                0,
                command="mealbot",
                args="pair",
                started_at=started,
            )
            assert result.returncode == 0, result.stderr
            assert len(proxy.requests) == 1
            mail_body = proxy.requests[0][1]
            assert b"a-cli-triple@example.test" in mail_body
            assert b"b-cli-triple@example.test" in mail_body
            assert b"c-cli-triple@example.test" in mail_body
        history = requests.get(
            f"{runtime.base_url}/pairs", params={"org": organization}, headers=runtime.auth, timeout=10
        )
        assert history.status_code == 200
        groups = history.json()["roundPairs"]
        assert len(groups) == 1 and len(groups[0]) == 1
        group = groups[0][0]
        emails = {
            group["member1"]["email"],
            group["member2"]["email"],
            group["extraMember"]["email"],
        }
        assert emails == {
            "a-cli-triple@example.test",
            "b-cli-triple@example.test",
            "c-cli-triple@example.test",
        }

    def test_cli_pair_mail_failure_does_not_persist(self, runtime: Runtime) -> None:
        organization = "ftrun_cli_pair_mail_failure"
        assert _create_org(runtime, organization, "cli-failure@example.test").status_code == 201
        members = "name,email\nAlice,a-cli-failure@example.test\nBob,b-cli-failure@example.test\n"
        assert _import_members(runtime, organization, members).status_code == 201
        assert _add_round(runtime, organization, "2026-04-01T00:00:00Z").status_code == 201
        with _local_mailgun(500) as (proxy, ca_cert):
            env = runtime.env.copy()
            env.update(
                {
                    "MAILGUN_SMTP_LOGIN": "bot@example.test",
                    "MAILGUN_DOMAIN": "example.test",
                    "MAILGUN_API_KEY": "functional-local-only",
                    "HTTPS_PROXY": f"http://127.0.0.1:{proxy.server_address[1]}",
                    "NO_PROXY": "",
                    "no_proxy": "",
                    "SSL_CERT_FILE": str(ca_cert),
                }
            )
            started = time.monotonic()
            result = _run([str(BINARY), "pair"], env=env)
            _record_cli(
                "test_cli_pair_mail_failure_does_not_persist",
                result,
                1,
                command="mealbot",
                args="pair",
                started_at=started,
            )
            assert result.returncode == 1
            assert len(proxy.requests) == 1
            assert "functional-local-only" not in result.stderr
        state = _psql(
            "SELECT done, (SELECT count(*) FROM pairs WHERE organization='ftrun_cli_pair_mail_failure') "
            "FROM rounds WHERE organization='ftrun_cli_pair_mail_failure' AND id=0;"
        ).strip()
        assert state == "f|0"

    def test_cli_invalid_argument(self, runtime: Runtime) -> None:
        started = time.monotonic()
        result = _run([str(BINARY), "invalid"], env=runtime.env)
        _record_cli(
            "test_cli_invalid_argument",
            result,
            1,
            command="mealbot",
            args="invalid",
            started_at=started,
        )
        assert result.returncode == 1
        assert result.stdout == ""
        assert result.stderr == "argument 'invalid' not recognized"

    def test_cli_migrate_legacy_triples(self, runtime: Runtime) -> None:
        database = "mealbot_legacy_ft_test"
        legacy_url = f"postgresql://morphagent@localhost/{database}?host=/var/run/postgresql"
        drop_before = _run(["sudo", "-u", "postgres", "dropdb", "--if-exists", "--force", database])
        assert drop_before.returncode == 0, drop_before.stderr
        created = _run(["sudo", "-u", "postgres", "createdb", "--owner=morphagent", database])
        assert created.returncode == 0, created.stderr
        try:
            _psql(
                """
                CREATE TABLE organizations (name text PRIMARY KEY, admin text NOT NULL, cross_match_trait text);
                CREATE TABLE members (organization text, email text, name text, metadata jsonb,
                    pair_counts jsonb NOT NULL, active boolean NOT NULL, PRIMARY KEY (organization,email));
                CREATE TABLE rounds (organization text, id integer, scheduled_date timestamp, done boolean,
                    PRIMARY KEY (organization,id));
                CREATE TABLE pairs (organization text, id1 text, id2 text, extraId text NOT NULL,
                    round integer, PRIMARY KEY (organization,id1,id2,extraId,round));
                INSERT INTO organizations VALUES ('legacy-functional', 'admin@example.test', NULL);
                INSERT INTO members VALUES
                    ('legacy-functional','a-legacy@example.test','A','{}','{}',true),
                    ('legacy-functional','b-legacy@example.test','B','{}','{}',true),
                    ('legacy-functional','c-legacy@example.test','C','{}','{}',false);
                INSERT INTO rounds VALUES ('legacy-functional',0,'2026-01-01 00:00:00',true);
                INSERT INTO pairs VALUES
                    ('legacy-functional','b-legacy@example.test','c-legacy@example.test','a-legacy@example.test',0),
                    ('legacy-functional','a-legacy@example.test','c-legacy@example.test','b-legacy@example.test',0),
                    ('legacy-functional','a-legacy@example.test','b-legacy@example.test','c-legacy@example.test',0);
                """,
                legacy_url,
            )
            env = runtime.env.copy()
            env["DATABASE_URL"] = legacy_url
            started = time.monotonic()
            result = _run([str(BINARY), "migrate"], env=env)
            _record_cli(
                "test_cli_migrate_legacy_triples",
                result,
                0,
                command="mealbot",
                args="migrate",
                started_at=started,
            )
            assert result.returncode == 0, result.stderr
            canonical = _psql(
                "SELECT id1 || ',' || id2 || ',' || extra_id || ',' || count(*) OVER () "
                "FROM pairs WHERE organization='legacy-functional' AND round=0;",
                legacy_url,
            ).strip()
            assert canonical == "a-legacy@example.test,b-legacy@example.test,c-legacy@example.test,1"
            symmetric = _psql(
                "SELECT last_round_with->>'b-legacy@example.test', "
                "last_round_with->>'c-legacy@example.test' FROM members "
                "WHERE organization='legacy-functional' AND email='a-legacy@example.test';",
                legacy_url,
            ).strip()
            assert symmetric == "0|0"
        finally:
            dropped = _run(["sudo", "-u", "postgres", "dropdb", "--if-exists", "--force", database])
            assert dropped.returncode == 0, dropped.stderr
