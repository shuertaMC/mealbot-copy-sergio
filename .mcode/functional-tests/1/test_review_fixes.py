import io, json, subprocess, time, uuid, glob
import jwt, pytest, requests

BASE = "http://localhost:8080"
KEY = open("/tmp/ftdeps/key.pem").read()
URI = open("/tmp/ftdeps/uri.txt").read().strip()
PSQL = glob.glob("/l2l/.local/lib/python3*/site-packages/pgserver/pginstall/bin/psql")[0]
RUN = uuid.uuid4().hex[:8]


def token():
    now = int(time.time())
    return jwt.encode({"iss": "https://mealbot.auth0.com/", "aud": "https://mealbot-2.herokuapp.com/",
                       "exp": now + 3600, "sub": "ft"}, KEY, algorithm="RS256", headers={"kid": "ft"})


H = {"Authorization": "Bearer " + token()}


def psql(sql):
    subprocess.run([PSQL, URI, "-v", "ON_ERROR_STOP=1", "-c", sql], check=True, capture_output=True)


@pytest.fixture(scope="module", autouse=True)
def health():
    assert requests.get(BASE + "/healthz", timeout=5).status_code == 200


@pytest.fixture
def org():
    name = f"ftrun_{RUN}_{uuid.uuid4().hex[:6]}"
    r = requests.post(BASE + "/org", params={"admin": "adm_" + name}, json={"org": name}, headers=H, timeout=10)
    assert r.status_code == 200
    yield name
    psql(f"DELETE FROM pairs WHERE organization='{name}'; DELETE FROM rounds WHERE organization='{name}'; "
         f"DELETE FROM members WHERE organization='{name}'; DELETE FROM organizations WHERE name='{name}';")


def req(method, path, **kw):
    kw.setdefault("headers", H)
    return requests.request(method, BASE + path, timeout=10, **kw)


def csv_files(content):
    return {"members": (f"m_{uuid.uuid4().hex}.csv", io.BytesIO(content.encode()), "text/csv")}


def test_auth_required():
    assert requests.get(BASE + "/orgs", params={"admin": "x"}, timeout=5).status_code == 401


# ---- organizations
def test_create_and_get_orgs(org):
    r = req("GET", "/orgs", params={"admin": "adm_" + org})
    assert r.status_code == 200 and r.json() == {"orgs": [org]}


def test_get_orgs_empty_admin_unknown():
    r = req("GET", "/orgs", params={"admin": "nobody_" + RUN})
    assert r.status_code == 200 and r.json() == {"orgs": []}


def test_get_orgs_missing_admin():
    r = req("GET", "/orgs")
    assert r.status_code == 400 and "must contain admin" in r.json()["Message"]


def test_get_orgs_duplicate_admin():
    r = req("GET", "/orgs?admin=a&admin=b")
    assert r.status_code == 400 and "must contain admin" in r.json()["Message"]


def test_create_org_missing_admin():
    r = req("POST", "/org", json={"org": "x" + RUN})
    assert r.status_code == 400 and "must contain admin" in r.json()["Message"]


def test_create_org_duplicate_admin():
    r = req("POST", "/org?admin=a&admin=b", json={"org": "x" + RUN})
    assert r.status_code == 400


def test_create_org_bad_body():
    r = req("POST", "/org", params={"admin": "a"}, data="notjson")
    assert r.status_code == 400


def test_create_org_wrong_method():
    assert req("GET", "/org", params={"admin": "a"}).status_code == 405


def test_create_org_empty_name():
    r = req("POST", "/org", params={"admin": "a"}, json={"org": ""})
    assert r.status_code == 500 and "empty" in r.json()["Message"]


def test_create_org_duplicate(org):
    r = req("POST", "/org", params={"admin": "a"}, json={"org": org})
    assert r.status_code == 500 and "duplicate key" in r.json()["Message"]


# ---- members
def test_post_members_traits(org):
    r = req("POST", "/members", params={"org": org}, files=csv_files("name,email,team\nA,a@x.com,t1\nB,b@x.com,t2\n"))
    assert r.status_code == 200
    j = r.json()
    assert len(j["members"]) == 2 and j["traits"] == ["team"]


def test_post_members_no_traits_is_empty_array(org):
    r = req("POST", "/members", params={"org": org}, files=csv_files("name,email\nA,a@x.com\n"))
    assert r.status_code == 200
    assert r.json()["traits"] == [] and '"traits":[]' in r.text


def test_post_members_header_only_traits_empty_array(org):
    r = req("POST", "/members", params={"org": org}, files=csv_files("name,email\n"))
    assert r.status_code == 200
    assert '"traits":[]' in r.text


def test_post_members_missing_org():
    r = req("POST", "/members", files=csv_files("name,email\nA,a@x.com\n"))
    assert r.status_code == 400 and "must contain org" in r.json()["Message"]


def test_post_members_no_file(org):
    r = req("POST", "/members", params={"org": org}, files={"other": ("o.csv", b"x")})
    assert r.status_code == 400


def test_post_members_bad_csv(org):
    r = req("POST", "/members", params={"org": org}, files=csv_files("foo,bar\n1,2\n"))
    assert r.status_code == 500


def test_get_members(org):
    req("POST", "/members", params={"org": org}, files=csv_files("name,email,team\nA,a@x.com,t1\n"))
    r = req("GET", "/members", params={"org": org})
    assert r.status_code == 200
    j = r.json()
    assert j["traits"] == ["team"] and len(j["members"]) == 1 and j["members"][0]["name"] == "A"


def test_get_members_empty_org(org):
    r = req("GET", "/members", params={"org": org})
    assert r.status_code == 200
    j = r.json()
    assert j["traits"] == [] and '"traits":[]' in r.text


def test_get_members_missing_org():
    assert req("GET", "/members").status_code == 400


def test_get_members_cross_match_trait(org):
    req("POST", "/members", params={"org": org}, files=csv_files("name,email,team\nA,a@x.com,t1\n"))
    assert req("POST", "/crossmatchtrait", params={"org": org}, json={"trait": "team"}).status_code == 200
    r = req("GET", "/members", params={"org": org})
    assert r.json()["crossMatchTrait"] == "team"


# ---- cross match trait
def test_crossmatch_set(org):
    r = req("POST", "/crossmatchtrait", params={"org": org}, json={"trait": "team"})
    assert r.status_code == 200 and "cross match" in r.json()["Message"]


def test_crossmatch_missing_org():
    r = req("POST", "/crossmatchtrait", json={"trait": "t"})
    assert r.status_code == 400 and "must contain org" in r.json()["Message"]


def test_crossmatch_bad_body(org):
    r = req("POST", "/crossmatchtrait", params={"org": org}, data="nope")
    assert r.status_code == 400 and r.json()["Message"] == "Request body is malformed"


def test_crossmatch_wrong_method(org):
    r = req("GET", "/crossmatchtrait", params={"org": org})
    assert r.status_code == 405 and "Message" in r.json()


# ---- pairs
def test_pairs_empty(org):
    r = req("GET", "/pairs", params={"org": org})
    assert r.status_code == 200 and r.json() == {"roundPairs": []}


def test_pairs_with_data(org):
    req("POST", "/members", params={"org": org}, files=csv_files("name,email\nA,a@x.com\nB,b@x.com\n"))
    psql(f"INSERT INTO rounds VALUES ('{org}',0,'2020-01-01 00:00:00',true); "
         f"INSERT INTO pairs VALUES ('{org}','a@x.com','b@x.com','',0);")
    r = req("GET", "/pairs", params={"org": org})
    assert r.status_code == 200
    rp = r.json()["roundPairs"]
    assert len(rp) == 1 and len(rp[0]) == 1


def test_pairs_missing_org():
    r = req("GET", "/pairs")
    assert r.status_code == 400 and "must contain org" in r.json()["Message"]


def test_pairs_wrong_method(org):
    assert req("POST", "/pairs", params={"org": org}).status_code == 405


# ---- rounds
def test_round_add_and_reschedule(org):
    assert req("POST", "/round", params={"org": org, "round": "2030-01-01T10:00:00Z"}).status_code == 200
    r = req("POST", "/round", params={"org": org, "round": "2031-01-01T10:00:00Z", "roundId": "0"})
    assert r.status_code == 200
    r = req("GET", "/rounds", params={"org": org})
    assert r.status_code == 200 and r.json()["rounds"] == ["2031-01-01 10:00:00Z"]


def test_round_reschedule_missing_round_param(org):
    r = req("POST", "/round", params={"org": org, "roundId": "0"})
    assert r.status_code == 400 and "must contain round" in r.json()["Message"]


def test_round_wrong_method(org):
    r = req("GET", "/round", params={"org": org})
    assert r.status_code == 405 and "Message" in r.json()


def test_round_add_missing_org():
    r = req("POST", "/round", params={"round": "2030-01-01T10:00:00Z"})
    assert r.status_code == 400 and "must contain org" in r.json()["Message"]
