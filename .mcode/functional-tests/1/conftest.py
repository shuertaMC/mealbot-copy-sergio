import base64, json, os, socket, subprocess, threading, time
from http.server import BaseHTTPRequestHandler, HTTPServer

import jwt
import pytest
import requests
from cryptography.hazmat.primitives.asymmetric import rsa

REPO = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", "..", ".."))
ISS = "https://ft.example/"
AUD = "https://ft-aud/"
KID = "ftkey"


def free_port():
    s = socket.socket(); s.bind(("127.0.0.1", 0)); p = s.getsockname()[1]; s.close(); return p


def b64(i):
    b = i.to_bytes((i.bit_length() + 7) // 8, "big")
    return base64.urlsafe_b64encode(b).rstrip(b"=").decode()


@pytest.fixture(scope="session")
def keypair():
    return rsa.generate_private_key(public_exponent=65537, key_size=2048)


@pytest.fixture(scope="session")
def jwks_url(keypair):
    pub = keypair.public_key().public_numbers()
    body = json.dumps({"keys": [{"kty": "RSA", "kid": KID, "use": "sig", "n": b64(pub.n), "e": b64(pub.e)}]}).encode()

    class H(BaseHTTPRequestHandler):
        def do_GET(self):
            self.send_response(200); self.send_header("Content-Type", "application/json"); self.end_headers(); self.wfile.write(body)
        def log_message(self, *a): pass

    port = free_port()
    srv = HTTPServer(("127.0.0.1", port), H)
    threading.Thread(target=srv.serve_forever, daemon=True).start()
    yield f"http://127.0.0.1:{port}/jwks.json"
    srv.shutdown()


@pytest.fixture(scope="session")
def db_url():
    import pgserver
    db = pgserver.get_server("/tmp/ft_pgdata", cleanup_mode="stop")
    uri = db.get_uri()
    with open(os.path.join(REPO, "schema.sql")) as f:
        db.psql(f.read())
    yield uri


@pytest.fixture(scope="session")
def base_url(db_url, jwks_url):
    port = free_port()
    binary = os.path.join(REPO, "mealbot")
    assert os.path.exists(binary), "build ./mealbot first"
    env = dict(os.environ, PORT=str(port), DATABASE_URL=db_url, AUTH0_ISSUER=ISS,
               AUTH0_AUDIENCE=AUD, AUTH0_JWKS_URL=jwks_url, STATIC_DIR=os.path.join(REPO, "static"))
    proc = subprocess.Popen([binary], cwd=REPO, env=env)
    url = f"http://127.0.0.1:{port}"
    for _ in range(50):
        try:
            if requests.get(url + "/healthz", timeout=1).status_code == 200:
                break
        except requests.RequestException:
            time.sleep(0.2)
    else:
        proc.kill(); pytest.fail("app never became healthy")
    yield url
    proc.terminate(); proc.wait(10)


def make_token(key, exp=True, expired=False, aud=AUD, iss=ISS):
    claims = {"iss": iss, "aud": aud, "sub": "ft"}
    if expired:
        claims["exp"] = int(time.time()) - 3600
    elif exp:
        claims["exp"] = int(time.time()) + 3600
    return jwt.encode(claims, key, algorithm="RS256", headers={"kid": KID})


@pytest.fixture(scope="session")
def auth(keypair):
    return {"Authorization": "Bearer " + make_token(keypair)}


@pytest.fixture(scope="session")
def tokfn(keypair):
    return lambda **kw: {"Authorization": "Bearer " + make_token(keypair, **kw)}
