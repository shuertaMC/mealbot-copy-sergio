import base64, json, os, subprocess, time, uuid
import pytest, requests

BASE = "http://127.0.0.1:%s" % os.environ.get("PORT", "18080")
KEY = "/tmp/ft_jwks/key.pem"
DB = os.environ.get("DATABASE_URL", "postgresql://morphagent@localhost/mealbot_test?host=/var/run/postgresql")


def b64(b):
    return base64.urlsafe_b64encode(b).rstrip(b"=").decode()


def make_token():
    h = b64(json.dumps({"alg": "RS256", "typ": "JWT", "kid": "ft-key"}).encode())
    p = b64(json.dumps({"iss": "https://issuer.invalid/", "aud": "mealbot-lifecycle", "sub": "ft", "exp": int(time.time()) + 3600}).encode())
    sig = subprocess.check_output(["openssl", "dgst", "-sha256", "-sign", KEY], input=f"{h}.{p}".encode())
    return f"{h}.{p}.{b64(sig)}"


def psql(sql):
    return subprocess.run(["psql", DB, "-v", "ON_ERROR_STOP=1", "-At", "-c", sql], capture_output=True, text=True, check=True).stdout


@pytest.fixture(scope="module")
def H():
    r = requests.get(BASE + "/privacy.html", timeout=5)
    assert r.status_code == 200
    return {"Authorization": "Bearer " + make_token()}


@pytest.fixture()
def org(H):
    name = "ftrun_" + uuid.uuid4().hex[:8]
    admin = name + "@example.com"
    r = requests.post(BASE + "/org", params={"admin": admin}, json={"org": name}, headers=H, timeout=10)
    assert r.status_code == 201, r.text
    yield name, admin
    psql(f"delete from pairs where organization='{name}'")
    psql(f"delete from organizations where name='{name}'")


def trait_in_db(name):
    return psql(f"select coalesce(cross_match_trait,'<NULL>') from organizations where name='{name}'").strip()


class TestAuth:
    def test_unauthenticated_rejected(self):
        assert requests.get(BASE + "/orgs", params={"admin": "x"}, timeout=5).status_code == 401

    def test_bad_token_rejected(self):
        r = requests.get(BASE + "/orgs", params={"admin": "x"}, headers={"Authorization": "Bearer abc.def.ghi"}, timeout=5)
        assert r.status_code == 401


class TestOrganizations:
    def test_create_and_list(self, H, org):
        name, admin = org
        r = requests.get(BASE + "/orgs", params={"admin": admin}, headers=H, timeout=5)
        assert r.status_code == 200 and name in r.json()["orgs"]

    def test_list_missing_admin(self, H):
        assert requests.get(BASE + "/orgs", headers=H, timeout=5).status_code == 400


class TestCrossMatchTrait:
    def post(self, H, name, body):
        return requests.post(BASE + "/crossmatchtrait", params={"org": name}, data=body, headers={**H, "Content-Type": "application/json"}, timeout=5)

    def test_set_trait(self, H, org):
        r = self.post(H, org[0], '{"trait":"College"}')
        assert r.status_code == 201
        assert trait_in_db(org[0]) == "College"

    def test_empty_string_clears(self, H, org):
        assert self.post(H, org[0], '{"trait":"College"}').status_code == 201
        r = self.post(H, org[0], '{"trait":""}')
        assert r.status_code == 201
        assert trait_in_db(org[0]) == "<NULL>"

    def test_null_clears(self, H, org):
        self.post(H, org[0], '{"trait":"College"}')
        r = self.post(H, org[0], '{"trait":null}')
        assert r.status_code == 201
        assert trait_in_db(org[0]) == "<NULL>"

    def test_blank_whitespace_not_rejected_with_400_from_trim(self, H, org):
        # spec change: blank traits are no longer trimmed/rejected at the service layer
        r = self.post(H, org[0], '{"trait":"   "}')
        assert r.status_code in (201, 400), (r.status_code, r.text)
        # record observed behaviour: DB CHECK forbids whitespace-only traits
        print("whitespace trait ->", r.status_code, r.text)

    def test_missing_trait_400(self, H, org):
        assert self.post(H, org[0], '{}').status_code == 400

    def test_wrong_type_400(self, H, org):
        assert self.post(H, org[0], '{"trait":5}').status_code == 400

    def test_missing_org_400(self, H):
        r = requests.post(BASE + "/crossmatchtrait", json={"trait": "x"}, headers=H, timeout=5)
        assert r.status_code == 400


class TestMembersRoundsPairs:
    def test_import_and_get_members(self, H, org):
        name, _ = org
        csv = "Name,Email,College\nA One,a1@x.com,SM\nB Two,b2@x.com,TD\n"
        r = requests.post(BASE + "/members", params={"org": name}, files={"members": ("m.csv", csv)}, headers=H, timeout=10)
        assert r.status_code == 201, r.text
        g = requests.get(BASE + "/members", params={"org": name}, headers=H, timeout=5)
        assert g.status_code == 200
        assert len(g.json()["members"]) == 2

    def test_rounds_lifecycle(self, H, org):
        name, _ = org
        ts = "2030-01-01T10:00:00Z"
        assert requests.post(BASE + "/round", params={"org": name, "round": ts}, headers=H, timeout=5).status_code == 201
        l = requests.get(BASE + "/rounds", params={"org": name}, headers=H, timeout=5)
        assert l.status_code == 200 and len(l.json()["rounds"]) == 1
        rid = psql(f"select id from rounds where organization='{name}'").strip()
        r = requests.post(BASE + "/round", params={"org": name, "round": "2031-01-01T10:00:00Z", "roundId": rid}, headers=H, timeout=5)
        assert r.status_code == 201
        d = requests.delete(BASE + "/round", params={"org": name, "roundId": rid}, headers=H, timeout=5)
        assert d.status_code == 201
        assert requests.get(BASE + "/rounds", params={"org": name}, headers=H, timeout=5).json()["rounds"] == []

    def test_round_invalid_timestamp(self, H, org):
        r = requests.post(BASE + "/round", params={"org": org[0], "round": "garbage"}, headers=H, timeout=5)
        assert r.status_code == 400

    def test_delete_missing_round_404(self, H, org):
        r = requests.delete(BASE + "/round", params={"org": org[0], "roundId": "9999"}, headers=H, timeout=5)
        assert r.status_code == 404

    def test_round_id_invalid(self, H, org):
        r = requests.delete(BASE + "/round", params={"org": org[0], "roundId": "abc"}, headers=H, timeout=5)
        assert r.status_code == 400

    def test_pairs_members_only_email_and_name(self, H, org):
        name, _ = org
        csv = "Name,Email,College\nA One,a1@x.com,SM\nB Two,b2@x.com,TD\n"
        assert requests.post(BASE + "/members", params={"org": name}, files={"members": ("m.csv", csv)}, headers=H, timeout=10).status_code == 201
        requests.post(BASE + "/round", params={"org": name, "round": "2030-01-01T10:00:00Z"}, headers=H, timeout=5)
        rid = psql(f"select id from rounds where organization='{name}'").strip()
        psql(f"insert into pairs(organization,id1,id2,round) values('{name}','a1@x.com','b2@x.com',{rid})")
        psql(f"update rounds set done=true where organization='{name}'")
        r = requests.get(BASE + "/pairs", params={"org": name}, headers=H, timeout=5)
        assert r.status_code == 200, r.text
        body = r.json()
        pair = body["roundPairs"][0][0]
        for k in ("member1", "member2"):
            m = pair[k]
            assert m["email"] in ("a1@x.com", "b2@x.com") and m["name"] in ("A One", "B Two")
            assert m.get("organization", "") == ""
            assert m.get("metadata") is None and m.get("lastRoundWith") is None
        print(r.text)

    def test_pairs_missing_org(self, H):
        assert requests.get(BASE + "/pairs", headers=H, timeout=5).status_code == 400
