import base64, datetime, json, os, subprocess, threading, time, uuid
from http.server import BaseHTTPRequestHandler, HTTPServer
import pytest, requests, jwt
from cryptography import x509
from cryptography.x509.oid import NameOID
from cryptography.hazmat.primitives import hashes, serialization
from cryptography.hazmat.primitives.asymmetric import rsa

REPO = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", ".."))
APP_PORT = 8091
JWKS_PORT = 8092
BASE = f"http://localhost:{APP_PORT}"
ISSUER = "https://ft.local/"
AUDIENCE = "https://ft.local/api"
PSQL = os.path.join(os.environ.get("PG_HOME", "C:/pgbin/pgsql"), "bin", "psql.exe")
DBURL = os.environ.get("DATABASE_URL", "postgres://postgres@localhost:5432/mealbot?sslmode=disable")
KID = "ftkid"
import shutil
GO = shutil.which("go") or "C:/Program Files/Go/bin/go.exe"

_key = rsa.generate_private_key(public_exponent=65537, key_size=2048)
_other = rsa.generate_private_key(public_exponent=65537, key_size=2048)


def _cert():
    name = x509.Name([x509.NameAttribute(NameOID.COMMON_NAME, "ft")])
    now = datetime.datetime.now(datetime.timezone.utc)
    c = (x509.CertificateBuilder().subject_name(name).issuer_name(name)
         .public_key(_key.public_key()).serial_number(1)
         .not_valid_before(now - datetime.timedelta(days=1))
         .not_valid_after(now + datetime.timedelta(days=30)).sign(_key, hashes.SHA256()))
    return base64.b64encode(c.public_bytes(serialization.Encoding.DER)).decode()


JWKS = json.dumps({"keys": [{"kty": "RSA", "kid": KID, "use": "sig", "x5c": [_cert()]}]}).encode()


class _H(BaseHTTPRequestHandler):
    def do_GET(self):
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.end_headers()
        self.wfile.write(JWKS)

    def log_message(self, *a):
        pass


def make_token(key=None, iss=ISSUER, aud=AUDIENCE, exp_delta=3600, kid=KID, alg="RS256"):
    claims = {"iss": iss, "aud": [aud], "sub": "ft", "exp": int(time.time()) + exp_delta}
    return jwt.encode(claims, key or _key, algorithm=alg, headers={"kid": kid})


def psql(sql):
    return subprocess.run([PSQL, DBURL, "-v", "ON_ERROR_STOP=1", "-tAc", sql], capture_output=True, text=True)


@pytest.fixture(scope="session")
def app():
    srv = HTTPServer(("127.0.0.1", JWKS_PORT), _H)
    threading.Thread(target=srv.serve_forever, daemon=True).start()
    exe = os.path.join(REPO, "ft_mealbot.exe")
    subprocess.run([GO, "build", "-o", exe, "./cmd/mealbot"], cwd=REPO, check=True)
    env = dict(os.environ, PORT=str(APP_PORT), AUTH0_ISSUER=ISSUER, AUTH0_AUDIENCE=AUDIENCE,
               AUTH0_JWKS_URL=f"http://127.0.0.1:{JWKS_PORT}/jwks.json", DATABASE_URL=DBURL)
    p = subprocess.Popen([exe], cwd=REPO, env=env, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    for _ in range(40):
        try:
            if requests.get(BASE + "/privacy.html", timeout=2).status_code == 200:
                break
        except Exception:
            time.sleep(0.5)
    else:
        p.kill()
        pytest.fail("app did not start")
    yield BASE
    p.kill()
    srv.shutdown()
    try:
        os.remove(exe)
    except OSError:
        pass


@pytest.fixture
def auth():
    return {"Authorization": "Bearer " + make_token()}


@pytest.fixture
def org(app, auth):
    name = "ftrun_" + uuid.uuid4().hex[:8]
    admin = name + "@ft.test"
    r = requests.post(f"{app}/org", params={"admin": admin}, json={"org": name}, headers=auth, timeout=10)
    assert r.status_code == 200, r.text
    yield name, admin
    for t in ("pairs", "rounds", "members"):
        psql(f"DELETE FROM {t} WHERE organization='{name}'")
    psql(f"DELETE FROM organizations WHERE name='{name}'")
