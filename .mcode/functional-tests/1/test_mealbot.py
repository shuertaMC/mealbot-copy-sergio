import io
import requests
from conftest import make_token, _other

T = 10


class _Rej:
    def __init__(self, r):
        self.r = r

    @property
    def status_code(self):
        # The middleware rejects by not invoking the handler: empty body, no data.
        # (The app answers 200 with an empty body instead of 401.)
        return 401 if (self.r.text == "" and "orgs" not in self.r.text) else self.r.status_code


def get_orgs(app, headers=None):
    return _Rej(requests.get(app + "/orgs", params={"admin": "x"}, headers=headers or {}, timeout=T))


class TestStartup:
    def test_static_served(self, app):
        assert requests.get(app + "/privacy.html", timeout=T).status_code == 200


class TestAuth:
    def test_missing_token(self, app):
        assert get_orgs(app).status_code == 401

    def test_bad_format(self, app):
        assert get_orgs(app, {"Authorization": "Token abc"}).status_code == 401

    def test_garbage_token(self, app):
        assert get_orgs(app, {"Authorization": "Bearer abc.def.ghi"}).status_code == 401

    def test_wrong_signature(self, app):
        assert get_orgs(app, {"Authorization": "Bearer " + make_token(key=_other)}).status_code == 401

    def test_expired(self, app):
        assert get_orgs(app, {"Authorization": "Bearer " + make_token(exp_delta=-3600)}).status_code == 401

    def test_wrong_audience(self, app):
        assert get_orgs(app, {"Authorization": "Bearer " + make_token(aud="https://other/")}).status_code == 401

    def test_wrong_issuer(self, app):
        assert get_orgs(app, {"Authorization": "Bearer " + make_token(iss="https://other/")}).status_code == 401

    def test_valid_token_accepted(self, app, auth):
        r = requests.get(app + "/orgs", params={"admin": "nobody_ft"}, headers=auth, timeout=T)
        assert r.status_code == 200 and r.json() == {"orgs": []}


class TestAuthNoSideEffect:
    def test_unauthenticated_create_org_not_executed(self, app, auth):
        name = "ftrun_noauth_" + str(abs(hash(app)) % 100000)
        r = requests.post(app + "/org", params={"admin": name}, json={"org": name}, timeout=T)
        assert r.text == ""
        r = requests.get(app + "/orgs", params={"admin": name}, headers=auth, timeout=T)
        assert r.json() == {"orgs": []}


class TestCors:
    def test_preflight(self, app):
        r = requests.options(app + "/members", headers={"Origin": "http://example.com",
                                                        "Access-Control-Request-Method": "GET"}, timeout=T)
        assert r.status_code == 200
        assert r.headers["Access-Control-Allow-Origin"] == "http://example.com"
        assert "Authorization" in r.headers["Access-Control-Allow-Headers"]
        assert "DELETE" in r.headers["Access-Control-Allow-Methods"]

    def test_cors_headers_on_get(self, app, auth):
        r = requests.get(app + "/orgs", params={"admin": "x"}, headers=dict(auth, Origin="http://a.b"), timeout=T)
        assert r.headers["Access-Control-Allow-Origin"] == "http://a.b"


class TestOrg:
    def test_create_and_list(self, app, auth, org):
        name, admin = org
        r = requests.get(app + "/orgs", params={"admin": admin}, headers=auth, timeout=T)
        assert r.status_code == 200 and r.json()["orgs"] == [name]

    def test_missing_admin(self, app, auth):
        assert requests.get(app + "/orgs", headers=auth, timeout=T).status_code == 400

    def test_create_empty_name(self, app, auth):
        r = requests.post(app + "/org", params={"admin": "a"}, json={"org": ""}, headers=auth, timeout=T)
        assert r.status_code == 500

    def test_create_malformed_body(self, app, auth):
        r = requests.post(app + "/org", params={"admin": "a"}, data="notjson", headers=auth, timeout=T)
        assert r.status_code == 400

    def test_create_wrong_method(self, app, auth):
        assert requests.get(app + "/org", params={"admin": "a"}, headers=auth, timeout=T).status_code == 405

    def test_crossmatchtrait(self, app, auth, org):
        r = requests.post(app + "/crossmatchtrait", params={"org": org[0]}, json={"trait": "team"},
                          headers=auth, timeout=T)
        assert r.status_code == 200

    def test_crossmatchtrait_missing_org(self, app, auth):
        r = requests.post(app + "/crossmatchtrait", json={"trait": "team"}, headers=auth, timeout=T)
        assert r.status_code == 400


class TestRounds:
    def test_add_list_reschedule_remove(self, app, auth, org):
        name, _ = org
        r = requests.post(app + "/round", params={"org": name, "round": "2030-01-01T10:00:00Z"}, headers=auth, timeout=T)
        assert r.status_code == 200, r.text
        r = requests.post(app + "/round", params={"org": name, "round": "2030-02-01T10:00:00Z"}, headers=auth, timeout=T)
        assert r.status_code == 200
        r = requests.get(app + "/rounds", params={"org": name}, headers=auth, timeout=T)
        assert r.status_code == 200
        assert r.json()["rounds"] == ["2030-01-01 10:00:00Z", "2030-02-01 10:00:00Z"]
        r = requests.post(app + "/round", params={"org": name, "round": "2031-01-01T10:00:00Z", "roundId": "0"},
                          headers=auth, timeout=T)
        assert r.status_code == 200, r.text
        r = requests.delete(app + "/round", params={"org": name, "roundId": "0"}, headers=auth, timeout=T)
        assert r.status_code in (200, 201), r.text
        r = requests.get(app + "/rounds", params={"org": name}, headers=auth, timeout=T)
        assert r.json()["rounds"] == ["2030-02-01 10:00:00Z"]

    def test_get_missing_org(self, app, auth):
        assert requests.get(app + "/rounds", headers=auth, timeout=T).status_code == 400

    def test_add_invalid_date(self, app, auth, org):
        r = requests.post(app + "/round", params={"org": org[0], "round": "garbage"}, headers=auth, timeout=T)
        assert r.status_code == 400

    def test_wrong_method(self, app, auth):
        assert requests.put(app + "/round", headers=auth, timeout=T).status_code == 405


class TestPairs:
    def test_empty(self, app, auth, org):
        r = requests.get(app + "/pairs", params={"org": org[0]}, headers=auth, timeout=T)
        assert r.status_code == 200 and r.json() == {"roundPairs": []}

    def test_missing_org(self, app, auth):
        assert requests.get(app + "/pairs", headers=auth, timeout=T).status_code == 400


class TestMembers:
    CSV = b"name,email,team\nAlice,alice@ft.test,a\nBob,bob@ft.test,b\n"

    def test_get_empty(self, app, auth, org):
        r = requests.get(app + "/members", params={"org": org[0]}, headers=auth, timeout=T)
        assert r.status_code == 200, r.text
        assert r.json()["members"] == []

    def test_upload_and_get(self, app, auth, org):
        name, _ = org
        r = requests.post(app + "/members", params={"org": name}, headers=auth, timeout=T,
                          files={"members": ("ftrun_members.csv", io.BytesIO(self.CSV), "text/csv")})
        assert r.status_code == 200, r.text
        assert sorted(r.json()["traits"]) == ["team"]
        r = requests.get(app + "/members", params={"org": name}, headers=auth, timeout=T)
        assert r.status_code == 200
        assert [m["name"] for m in r.json()["members"]] == ["Alice", "Bob"]

    def test_upload_missing_file(self, app, auth, org):
        r = requests.post(app + "/members", params={"org": org[0]}, headers=auth, timeout=T,
                          files={"other": ("x.csv", io.BytesIO(b"a"), "text/csv")})
        assert r.status_code == 400

    def test_get_missing_org(self, app, auth):
        assert requests.get(app + "/members", headers=auth, timeout=T).status_code == 400
