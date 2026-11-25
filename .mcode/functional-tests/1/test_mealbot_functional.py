import base64
import datetime as dt
import email.parser
import email.policy
import json
import os
import socketserver
import ssl
import subprocess
import threading
import time
import urllib.parse
import uuid
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

import pytest
import requests
from cryptography import x509
from cryptography.hazmat.primitives import hashes, serialization
from cryptography.hazmat.primitives.asymmetric import padding, rsa
from cryptography.x509.oid import NameOID


REPO = Path(os.environ["WORKSPACE_DIR"]) / "mealbot-copy-sergio"
BASE_URL = "http://127.0.0.1:18080"
DATABASE_URL = os.environ.get(
    "DATABASE_URL",
    "postgres:///mealbot_lifecycle_test?host=/var/run/postgresql&sslmode=disable",
)
ISSUER = "http://127.0.0.1:18081/"
AUDIENCE = "http://mealbot.functional/"
JWKS_PORT = 18081
EVIDENCE = REPO / ".mcode/functional-tests/1/evidence.jsonl"


def _b64url(value: bytes) -> str:
    return base64.urlsafe_b64encode(value).rstrip(b"=").decode()


def _evidence(name, *, output, status=None, exit_code=None):
    entry = {"name": name, "actual_output": output}
    if status is not None:
        entry["actual_status"] = status
    if exit_code is not None:
        entry["actual_exit_code"] = exit_code
    with EVIDENCE.open("a", encoding="utf-8") as stream:
        stream.write(json.dumps(entry, ensure_ascii=False) + "\n")


def _psql(sql: str) -> str:
    result = subprocess.run(
        ["psql", DATABASE_URL, "--no-psqlrc", "--tuples-only", "--no-align", "--command", sql],
        cwd=REPO,
        text=True,
        capture_output=True,
        timeout=20,
    )
    assert result.returncode == 0, result.stderr
    return result.stdout.strip()


def _cleanup_org(org: str):
    assert org.replace("_", "").isalnum()
    _psql(
        "BEGIN; "
        f"DELETE FROM pairs WHERE organization='{org}'; "
        f"DELETE FROM rounds WHERE organization='{org}'; "
        f"DELETE FROM members WHERE organization='{org}'; "
        f"DELETE FROM organizations WHERE name='{org}'; "
        "COMMIT;"
    )


@pytest.fixture(scope="session")
def auth_token():
    EVIDENCE.unlink(missing_ok=True)
    private_key = rsa.generate_private_key(public_exponent=65537, key_size=2048)
    public = private_key.public_key().public_numbers()
    jwks = {
        "keys": [
            {
                "kty": "RSA",
                "kid": "functional-key",
                "use": "sig",
                "alg": "RS256",
                "n": _b64url(public.n.to_bytes((public.n.bit_length() + 7) // 8, "big")),
                "e": _b64url(public.e.to_bytes((public.e.bit_length() + 7) // 8, "big")),
            }
        ]
    }

    class JWKSHandler(BaseHTTPRequestHandler):
        def do_GET(self):
            if self.path != "/jwks.json":
                self.send_error(404)
                return
            encoded = json.dumps(jwks).encode()
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(encoded)))
            self.end_headers()
            self.wfile.write(encoded)

        def log_message(self, _format, *_args):
            return

    server = ThreadingHTTPServer(("127.0.0.1", JWKS_PORT), JWKSHandler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()

    now = int(time.time())
    header = _b64url(json.dumps({"alg": "RS256", "kid": "functional-key", "typ": "JWT"}, separators=(",", ":")).encode())
    claims = _b64url(
        json.dumps(
            {"iss": ISSUER, "aud": AUDIENCE, "sub": "functional-admin", "iat": now, "exp": now + 3600},
            separators=(",", ":"),
        ).encode()
    )
    signing_input = f"{header}.{claims}".encode()
    signature = private_key.sign(signing_input, padding.PKCS1v15(), hashes.SHA256())
    token = f"{header}.{claims}.{_b64url(signature)}"
    yield token
    server.shutdown()
    server.server_close()
    thread.join(timeout=5)


@pytest.fixture(autouse=True)
def live_app(auth_token):
    response = requests.get(f"{BASE_URL}/privacy.html", timeout=5)
    assert response.status_code == 200


@pytest.fixture
def api(auth_token):
    session = requests.Session()
    session.headers["Authorization"] = f"Bearer {auth_token}"
    return session


@pytest.fixture
def org_factory(api):
    organizations = []

    def create(label: str):
        org = f"ftrun_{label}_{uuid.uuid4().hex[:8]}"
        response = api.post(
            f"{BASE_URL}/org",
            params={"admin": "functional-admin@example.test"},
            json={"org": org},
            timeout=10,
        )
        assert response.status_code == 200, response.text
        organizations.append(org)
        return org

    yield create
    for organization in reversed(organizations):
        _cleanup_org(organization)


def _upload(api, org, csv_text):
    return api.post(
        f"{BASE_URL}/members",
        params={"org": org},
        files={"members": ("members.csv", csv_text, "text/csv")},
        timeout=15,
    )


class _MailgunProxy:
    def __init__(self, tmp_path: Path, response_status: int):
        self.response_status = response_status
        self.records = []
        self._lock = threading.Lock()
        self._make_certificates(tmp_path)
        proxy = self

        class Handler(socketserver.BaseRequestHandler):
            def handle(self):
                connect = self._read_until(self.request, b"\r\n\r\n")
                if not connect.startswith(b"CONNECT api.mailgun.net:443 HTTP/1."):
                    return
                self.request.sendall(b"HTTP/1.1 200 Connection Established\r\n\r\n")
                context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
                context.load_cert_chain(proxy.cert_file, proxy.key_file)
                with context.wrap_socket(self.request, server_side=True) as connection:
                    raw_headers = self._read_until(connection, b"\r\n\r\n")
                    if not raw_headers:
                        return
                    head, initial = raw_headers.split(b"\r\n\r\n", 1)
                    lines = head.split(b"\r\n")
                    method, path, _version = lines[0].decode().split(" ", 2)
                    headers = {}
                    for line in lines[1:]:
                        key, value = line.split(b":", 1)
                        headers[key.decode().lower()] = value.decode().strip()
                    body = initial
                    if headers.get("transfer-encoding", "").lower() == "chunked":
                        body += self._read_chunked(connection)
                    else:
                        needed = int(headers.get("content-length", "0")) - len(body)
                        while needed > 0:
                            chunk = connection.recv(needed)
                            if not chunk:
                                break
                            body += chunk
                            needed -= len(chunk)
                    fields = self._multipart(headers.get("content-type", ""), body)
                    snapshot = {
                        "method": method,
                        "path": path,
                        "authorization": headers.get("authorization", ""),
                        "fields": fields,
                        "pairs_before_response": int(_psql("SELECT count(*) FROM pairs")),
                        "done_before_response": int(_psql("SELECT count(*) FROM rounds WHERE done=TRUE")),
                    }
                    with proxy._lock:
                        proxy.records.append(snapshot)
                    payload = b'{"message":"Queued. Thank you","id":"<functional@mailgun.test>"}'
                    reason = b"OK" if proxy.response_status == 200 else b"Internal Server Error"
                    connection.sendall(
                        b"HTTP/1.1 "
                        + str(proxy.response_status).encode()
                        + b" "
                        + reason
                        + b"\r\nContent-Type: application/json\r\nContent-Length: "
                        + str(len(payload)).encode()
                        + b"\r\nConnection: close\r\n\r\n"
                        + payload
                    )

            @staticmethod
            def _read_until(connection, marker):
                data = b""
                while marker not in data and len(data) < 1_000_000:
                    chunk = connection.recv(65536)
                    if not chunk:
                        break
                    data += chunk
                return data

            @staticmethod
            def _read_chunked(connection):
                data = b""
                while True:
                    line = b""
                    while not line.endswith(b"\r\n"):
                        line += connection.recv(1)
                    size = int(line.strip().split(b";", 1)[0], 16)
                    if size == 0:
                        _ = connection.recv(2)
                        break
                    chunk = b""
                    while len(chunk) < size:
                        chunk += connection.recv(size - len(chunk))
                    data += chunk
                    _ = connection.recv(2)
                return data

            @staticmethod
            def _multipart(content_type, body):
                if "multipart/" not in content_type:
                    return {}
                document = email.parser.BytesParser(policy=email.policy.default).parsebytes(
                    f"Content-Type: {content_type}\r\nMIME-Version: 1.0\r\n\r\n".encode() + body
                )
                fields = {}
                for part in document.iter_parts():
                    name = part.get_param("name", header="content-disposition")
                    fields.setdefault(name, []).append(part.get_content())
                return fields

        self.server = socketserver.ThreadingTCPServer(("127.0.0.1", 0), Handler)
        self.server.daemon_threads = True
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()
        self.url = f"http://127.0.0.1:{self.server.server_address[1]}"

    def _make_certificates(self, tmp_path):
        ca_key = rsa.generate_private_key(public_exponent=65537, key_size=2048)
        ca_name = x509.Name([x509.NameAttribute(NameOID.COMMON_NAME, "Mealbot Functional CA")])
        now = dt.datetime.utcnow()
        ca_cert = (
            x509.CertificateBuilder()
            .subject_name(ca_name)
            .issuer_name(ca_name)
            .public_key(ca_key.public_key())
            .serial_number(x509.random_serial_number())
            .not_valid_before(now - dt.timedelta(minutes=1))
            .not_valid_after(now + dt.timedelta(days=1))
            .add_extension(x509.BasicConstraints(ca=True, path_length=None), critical=True)
            .sign(ca_key, hashes.SHA256())
        )
        server_key = rsa.generate_private_key(public_exponent=65537, key_size=2048)
        server_name = x509.Name([x509.NameAttribute(NameOID.COMMON_NAME, "api.mailgun.net")])
        server_cert = (
            x509.CertificateBuilder()
            .subject_name(server_name)
            .issuer_name(ca_name)
            .public_key(server_key.public_key())
            .serial_number(x509.random_serial_number())
            .not_valid_before(now - dt.timedelta(minutes=1))
            .not_valid_after(now + dt.timedelta(days=1))
            .add_extension(x509.SubjectAlternativeName([x509.DNSName("api.mailgun.net")]), critical=False)
            .sign(ca_key, hashes.SHA256())
        )
        self.ca_file = str(tmp_path / f"ca-{uuid.uuid4().hex}.pem")
        self.cert_file = str(tmp_path / f"server-{uuid.uuid4().hex}.pem")
        self.key_file = str(tmp_path / f"server-{uuid.uuid4().hex}.key")
        Path(self.ca_file).write_bytes(ca_cert.public_bytes(serialization.Encoding.PEM))
        Path(self.cert_file).write_bytes(server_cert.public_bytes(serialization.Encoding.PEM))
        Path(self.key_file).write_bytes(
            server_key.private_bytes(
                serialization.Encoding.PEM,
                serialization.PrivateFormat.TraditionalOpenSSL,
                serialization.NoEncryption(),
            )
        )

    def close(self):
        self.server.shutdown()
        self.server.server_close()
        self.thread.join(timeout=5)


@pytest.fixture
def mailgun_proxy_factory(tmp_path):
    proxies = []

    def create(status=200):
        proxy = _MailgunProxy(tmp_path, status)
        proxies.append(proxy)
        return proxy

    yield create
    for proxy in proxies:
        proxy.close()


def _pair_command(proxy):
    env = os.environ.copy()
    env.update(
        {
            "DATABASE_URL": DATABASE_URL,
            "AUTH0_ISSUER": ISSUER,
            "AUTH0_AUDIENCE": AUDIENCE,
            "AUTH0_JWKS_URL": f"{ISSUER}jwks.json",
            "MAILGUN_DOMAIN": "functional.test",
            "MAILGUN_API_KEY": "functional-key",
            "MAILGUN_SMTP_LOGIN": "sender@functional.test",
            "MEALBOT_TEST_MODE": "false",
            "HTTPS_PROXY": proxy.url,
            "https_proxy": proxy.url,
            "SSL_CERT_FILE": proxy.ca_file,
        }
    )
    env.pop("NO_PROXY", None)
    env.pop("no_proxy", None)
    return subprocess.run(
        [str(REPO / "bin/mealbot"), "pair"],
        cwd=REPO,
        env=env,
        text=True,
        capture_output=True,
        timeout=30,
    )


class TestPublicAndAuth:
    def test_static_assets_are_public_and_exact(self):
        privacy = requests.get(f"{BASE_URL}/privacy.html", timeout=10)
        sample = requests.get(f"{BASE_URL}/sample.csv", timeout=10)
        _evidence("test_static_assets_are_public_and_exact", output=privacy.text, status=privacy.status_code)
        assert privacy.status_code == 200
        assert privacy.content == (REPO / "static/privacy.html").read_bytes()
        assert sample.status_code == 200
        assert sample.content == (REPO / "static/sample.csv").read_bytes()

    def test_auth_rejection_and_options_cors(self):
        missing = requests.get(f"{BASE_URL}/orgs", params={"admin": "nobody"}, timeout=10)
        invalid = requests.get(
            f"{BASE_URL}/orgs",
            params={"admin": "nobody"},
            headers={"Authorization": "Bearer invalid"},
            timeout=10,
        )
        preflight = requests.options(
            f"{BASE_URL}/members",
            headers={"Origin": "https://admin.functional.test"},
            timeout=10,
        )
        _evidence("test_auth_rejection_and_options_cors", output=preflight.text, status=preflight.status_code)
        assert (missing.status_code, missing.text) == (200, "")
        assert (invalid.status_code, invalid.text) == (200, "")
        assert (preflight.status_code, preflight.text) == (200, "")
        assert preflight.headers["Access-Control-Allow-Origin"] == "https://admin.functional.test"
        assert preflight.headers["Access-Control-Allow-Headers"] == "Authorization, Content-Type, Origin, Accept, token"
        assert preflight.headers["Access-Control-Allow-Methods"] == "GET, POST, DELETE"


class TestOrganizationAndMembers:
    def test_organization_create_list_and_crossmatch(self, api, org_factory):
        org = org_factory("organization")
        listed = api.get(
            f"{BASE_URL}/orgs",
            params={"admin": "functional-admin@example.test"},
            timeout=10,
        )
        trait = api.post(
            f"{BASE_URL}/crossmatchtrait",
            params={"org": org},
            json={"trait": "college"},
            timeout=10,
        )
        _evidence("test_organization_create_list_and_crossmatch", output=trait.text, status=trait.status_code)
        assert listed.status_code == 200
        assert org in listed.json()["orgs"]
        assert (trait.status_code, trait.json()) == (200, {"Message": "Successfully set the cross match trait"})

    def test_member_csv_sync_deactivate_and_reactivate(self, api, org_factory):
        org = org_factory("members")
        first = _upload(
            api,
            org,
            "NAME,EMAIL,College,Year\n Alice ,alice@example.test,North,2027\nBob,bob@example.test,South,2028\nCara,cara@example.test,North,2029\n",
        )
        second = _upload(
            api,
            org,
            "name,email,COLLEGE,year\nAlice Updated,alice@example.test,East,2030\nBob,bob@example.test,South,2028\n",
        )
        after_deactivate = api.get(f"{BASE_URL}/members", params={"org": org}, timeout=10)
        third = _upload(
            api,
            org,
            "name,email,college,year\nAlice Updated,alice@example.test,East,2030\nBob,bob@example.test,South,2028\nCara,cara@example.test,West,2031\n",
        )
        final = api.get(f"{BASE_URL}/members", params={"org": org}, timeout=10)
        _evidence("test_member_csv_sync_deactivate_and_reactivate", output=final.text, status=final.status_code)
        assert first.status_code == second.status_code == third.status_code == 200
        assert {m["email"] for m in after_deactivate.json()["members"]} == {
            "alice@example.test",
            "bob@example.test",
        }
        body = final.json()
        assert {m["email"] for m in body["members"]} == {
            "alice@example.test",
            "bob@example.test",
            "cara@example.test",
        }
        assert body["traits"] == ["college", "year"]
        assert "crossMatchTrait" not in body
        alice = next(member for member in body["members"] if member["email"] == "alice@example.test")
        assert alice == {"name": "Alice Updated", "email": "alice@example.test", "college": "East", "year": "2030"}

    def test_invalid_csv_rolls_back_entire_sync(self, api, org_factory):
        org = org_factory("csvrollback")
        valid = _upload(api, org, "name,email,team\nAlpha,alpha@example.test,A\nBeta,beta@example.test,B\n")
        before = api.get(f"{BASE_URL}/members", params={"org": org}, timeout=10).text
        invalid = _upload(api, org, "name,email,team\nChanged,alpha@example.test,A\nmalformed,row\n")
        after = api.get(f"{BASE_URL}/members", params={"org": org}, timeout=10).text
        _evidence("test_invalid_csv_rolls_back_entire_sync", output=invalid.text, status=invalid.status_code)
        assert valid.status_code == 200
        assert invalid.status_code == 500
        assert before == after


class TestRoundsAndPairing:
    def test_round_add_reschedule_delete_and_renumber(self, api, org_factory):
        org = org_factory("rounds")
        for scheduled in ("2030-01-01 10:00:00Z", "2030-01-02 11:00:00Z", "2030-01-03 12:00:00Z"):
            response = api.post(f"{BASE_URL}/round", params={"org": org, "round": scheduled}, timeout=10)
            assert response.status_code == 200, response.text
        changed = api.post(
            f"{BASE_URL}/round",
            params={"org": org, "round": "2031-02-03 13:14:15Z", "roundId": "1"},
            timeout=10,
        )
        removed = api.delete(f"{BASE_URL}/round", params={"org": org, "roundId": "0"}, timeout=10)
        listed = api.get(f"{BASE_URL}/rounds", params={"org": org}, timeout=10)
        _evidence("test_round_add_reschedule_delete_and_renumber", output=removed.text, status=removed.status_code)
        assert changed.status_code == removed.status_code == listed.status_code == 200
        assert listed.json() == {"rounds": ["2031-02-03 13:14:15Z", "2030-01-03 12:00:00Z"]}
        assert _psql(f"SELECT string_agg(id::text, ',' ORDER BY id) FROM rounds WHERE organization='{org}'") == "0,1"

    def test_pair_cli_delivers_then_persists_and_exposes_history(self, api, org_factory, mailgun_proxy_factory):
        org = org_factory("pairsuccess")
        uploaded = _upload(
            api,
            org,
            "name,email,team\nAlpha,alpha@example.test,A\nBeta,beta@example.test,A\nGamma,gamma@example.test,B\nDelta,delta@example.test,B\n",
        )
        scheduled = api.post(
            f"{BASE_URL}/round",
            params={"org": org, "round": "2000-01-01 00:00:00Z"},
            timeout=10,
        )
        assert uploaded.status_code == scheduled.status_code == 200
        proxy = mailgun_proxy_factory(200)
        result = _pair_command(proxy)
        history = api.get(f"{BASE_URL}/pairs", params={"org": org}, timeout=10)
        _evidence(
            "test_pair_cli_delivers_then_persists_and_exposes_history",
            output=result.stdout,
            exit_code=result.returncode,
        )
        assert result.returncode == 0, result.stderr
        assert len(proxy.records) == 2
        assert all(record["method"] == "POST" for record in proxy.records)
        assert all(record["path"] == "/v3/functional.test/messages" for record in proxy.records)
        assert all(record["pairs_before_response"] == 0 for record in proxy.records)
        assert all(record["done_before_response"] == 0 for record in proxy.records)
        for record in proxy.records:
            fields = record["fields"]
            assert fields["subject"] == ["Your new Mealbot group"]
            assert fields["from"] == [f"{org} Mealbot <sender@functional.test>"]
            assert len(fields["to"]) == 2
            assert "Your Mealbot group this week is:" in fields["text"][0]
            assert "Sent by your friendly neighborhood Mealbot!" in fields["text"][0]
        assert _psql(f"SELECT count(*) FROM pairs WHERE organization='{org}'") == "2"
        assert _psql(f"SELECT done::text FROM rounds WHERE organization='{org}' AND id=0") == "true"
        assert history.status_code == 200
        assert len(history.json()["roundPairs"]) == 1
        assert len(history.json()["roundPairs"][0]) == 2
        histories = _psql(f"SELECT count(*) FROM members WHERE organization='{org}' AND (last_round_with::text LIKE '%: 0%')")
        assert histories == "4"

    def test_mailgun_failure_leaves_round_and_history_uncommitted(self, api, org_factory, mailgun_proxy_factory):
        org = org_factory("pairfailure")
        assert _upload(api, org, "name,email\nAlpha,alpha-fail@example.test\nBeta,beta-fail@example.test\n").status_code == 200
        assert api.post(
            f"{BASE_URL}/round",
            params={"org": org, "round": "2000-01-01 00:00:00Z"},
            timeout=10,
        ).status_code == 200
        before = _psql(f"SELECT string_agg(last_round_with::text, '|' ORDER BY email) FROM members WHERE organization='{org}'")
        proxy = mailgun_proxy_factory(500)
        result = _pair_command(proxy)
        after = _psql(f"SELECT string_agg(last_round_with::text, '|' ORDER BY email) FROM members WHERE organization='{org}'")
        _evidence(
            "test_mailgun_failure_leaves_round_and_history_uncommitted",
            output=result.stderr,
            exit_code=result.returncode,
        )
        assert result.returncode == 1
        assert len(proxy.records) == 1
        assert _psql(f"SELECT count(*) FROM pairs WHERE organization='{org}'") == "0"
        assert _psql(f"SELECT done::text FROM rounds WHERE organization='{org}' AND id=0") == "false"
        assert before == after


class TestCLI:
    def test_migrate_is_idempotent(self):
        env = os.environ.copy()
        env["DATABASE_URL"] = DATABASE_URL
        first = subprocess.run(
            [str(REPO / "bin/mealbot"), "migrate"],
            cwd=REPO,
            env=env,
            text=True,
            capture_output=True,
            timeout=30,
        )
        second = subprocess.run(
            [str(REPO / "bin/mealbot"), "migrate"],
            cwd=REPO,
            env=env,
            text=True,
            capture_output=True,
            timeout=30,
        )
        _evidence("test_migrate_is_idempotent", output=second.stdout, exit_code=second.returncode)
        assert first.returncode == second.returncode == 0
        assert first.stdout == second.stdout == "Done migrating to 'last round with' data structure in pairing algorithm!\n"

    def test_unknown_argument_preserves_legacy_output(self):
        result = subprocess.run(
            [str(REPO / "bin/mealbot"), "functional-unknown"],
            cwd=REPO,
            text=True,
            capture_output=True,
            timeout=10,
        )
        _evidence("test_unknown_argument_preserves_legacy_output", output=result.stdout, exit_code=result.returncode)
        assert result.returncode == 0
        assert result.stdout == "argument 'functional-unknown' not recognized"
        assert result.stderr == ""
