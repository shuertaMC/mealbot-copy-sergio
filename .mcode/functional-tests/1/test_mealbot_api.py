import uuid
import requests

T = 10


def org():
    return "ftrun_" + uuid.uuid4().hex[:8]


class TestHealthAndAuth:
    def test_healthz(self, base_url):
        r = requests.get(base_url + "/healthz", timeout=T)
        assert r.status_code == 200 and r.text == "ok"

    def test_no_token_401(self, base_url):
        r = requests.get(base_url + "/orgs?admin=a", timeout=T)
        assert r.status_code == 401 and "Invalid access token" in r.text

    def test_expired_token_401(self, base_url, tokfn):
        r = requests.get(base_url + "/orgs?admin=a", headers=tokfn(expired=True), timeout=T)
        assert r.status_code == 401

    def test_token_without_exp_accepted(self, base_url, tokfn):
        r = requests.get(base_url + "/orgs?admin=noexp", headers=tokfn(exp=False), timeout=T)
        assert r.status_code == 200 and r.json() == {"orgs": []}

    def test_wrong_audience_401(self, base_url, tokfn):
        r = requests.get(base_url + "/orgs?admin=a", headers=tokfn(aud="other"), timeout=T)
        assert r.status_code == 401


class TestOrgs:
    def test_create_and_list_org(self, base_url, auth):
        admin = "adm_" + org()
        name = org()
        r = requests.post(base_url + f"/org?admin={admin}", json={"org": name}, headers=auth, timeout=T)
        assert r.status_code == 200 and r.json() == {"Message": "Successfully created new organization"}
        r = requests.get(base_url + f"/orgs?admin={admin}", headers=auth, timeout=T)
        assert r.status_code == 200 and r.json() == {"orgs": [name]}

    def test_orgs_missing_admin(self, base_url, auth):
        r = requests.get(base_url + "/orgs", headers=auth, timeout=T)
        assert r.status_code == 400 and "request query parameters must contain 'admin'" in r.text

    def test_orgs_duplicate_admin_param(self, base_url, auth):
        r = requests.get(base_url + "/orgs?admin=a&admin=b", headers=auth, timeout=T)
        assert r.status_code == 400

    def test_create_org_bad_body(self, base_url, auth):
        r = requests.post(base_url + "/org?admin=x", data="notjson", headers=auth, timeout=T)
        assert r.status_code == 400

    def test_create_org_missing_admin(self, base_url, auth):
        r = requests.post(base_url + "/org", json={"org": "x"}, headers=auth, timeout=T)
        assert r.status_code == 400 and "'admin'" in r.text

    def test_crossmatchtrait_flow(self, base_url, auth):
        admin, name = "adm_" + org(), org()
        requests.post(base_url + f"/org?admin={admin}", json={"org": name}, headers=auth, timeout=T)
        r = requests.post(base_url + f"/crossmatchtrait?org={name}", json={"trait": "team"}, headers=auth, timeout=T)
        assert r.status_code == 200, r.text
        r = requests.get(base_url + f"/members?org={name}", headers=auth, timeout=T)
        assert r.status_code == 200 and r.json()["crossMatchTrait"] == "team"

    def test_crossmatchtrait_missing_org(self, base_url, auth):
        r = requests.post(base_url + "/crossmatchtrait", json={"trait": "t"}, headers=auth, timeout=T)
        assert r.status_code == 400 and r.text.endswith("Request query parameters must contain org") or "must contain org" in r.text

    def test_crossmatchtrait_malformed_body(self, base_url, auth):
        r = requests.post(base_url + "/crossmatchtrait?org=x", data="{bad", headers=auth, timeout=T)
        assert r.status_code == 400 and "Request body is malformed" in r.text


class TestMembersRoundsPairs:
    def _setup_org(self, base_url, auth):
        admin, name = "adm_" + org(), org()
        requests.post(base_url + f"/org?admin={admin}", json={"org": name}, headers=auth, timeout=T)
        return name

    def test_members_upload_and_get(self, base_url, auth):
        name = self._setup_org(base_url, auth)
        csv = "name,email,team\nAl,al@x.com,a\nBo,bo@x.com,b\n"
        r = requests.post(base_url + f"/members?org={name}", files={"members": ("m.csv", csv)}, headers=auth, timeout=T)
        assert r.status_code == 200, r.text
        j = r.json()
        assert len(j["members"]) == 2 and j["traits"] == ["team"]
        r = requests.get(base_url + f"/members?org={name}", headers=auth, timeout=T)
        assert r.status_code == 200
        assert {m["email"] for m in r.json()["members"]} == {"al@x.com", "bo@x.com"}

    def test_members_csv_no_name_column(self, base_url, auth):
        name = self._setup_org(base_url, auth)
        r = requests.post(base_url + f"/members?org={name}", files={"members": ("m.csv", "email\na@x.com\n")}, headers=auth, timeout=T)
        assert r.status_code == 400 and "CSV must have a column titled 'name'" in r.text

    def test_members_missing_org(self, base_url, auth):
        assert requests.get(base_url + "/members", headers=auth, timeout=T).status_code == 400
        assert requests.post(base_url + "/members", headers=auth, timeout=T).status_code == 400

    def test_members_post_no_file(self, base_url, auth):
        r = requests.post(base_url + "/members?org=x", data="x", headers=auth, timeout=T)
        assert r.status_code == 400

    def test_rounds_flow(self, base_url, auth):
        name = self._setup_org(base_url, auth)
        r = requests.post(base_url + f"/round?org={name}&round=2031-01-02T03:04:05Z", headers=auth, timeout=T)
        assert r.status_code == 200 and r.json() == {"Message": "Successfully scheduled a new round"}
        r = requests.get(base_url + f"/rounds?org={name}", headers=auth, timeout=T)
        assert r.status_code == 200 and r.json() == {"rounds": ["2031-01-02 03:04:05Z"]}
        r = requests.post(base_url + f"/round?org={name}&round=2032-02-03T04:05:06Z&roundId=0", headers=auth, timeout=T)
        assert r.status_code == 200 and r.json() == {"Message": "Succesfully changed date of the round"}
        r = requests.get(base_url + f"/rounds?org={name}", headers=auth, timeout=T)
        assert r.json() == {"rounds": ["2032-02-03 04:05:06Z"]}
        r = requests.delete(base_url + f"/round?org={name}&roundId=0", headers=auth, timeout=T)
        assert r.status_code == 200 and r.json() == {"Message": "Successfully removed round"}
        r = requests.get(base_url + f"/rounds?org={name}", headers=auth, timeout=T)
        assert r.json() == {"rounds": []}

    def test_rounds_missing_org(self, base_url, auth):
        r = requests.get(base_url + "/rounds", headers=auth, timeout=T)
        assert r.status_code == 400 and "Request query parameters must contain org" in r.text

    def test_round_missing_params(self, base_url, auth):
        r = requests.post(base_url + "/round?org=x", headers=auth, timeout=T)
        assert r.status_code == 400 and "does not contain round" in r.text

    def test_round_bad_round_id(self, base_url, auth):
        r = requests.post(base_url + "/round?org=x&round=2031-01-01T00:00:00Z&roundId=abc", headers=auth, timeout=T)
        assert r.status_code == 400
        r = requests.delete(base_url + "/round?org=x&roundId=abc", headers=auth, timeout=T)
        assert r.status_code == 400

    def test_pairs_empty(self, base_url, auth):
        name = self._setup_org(base_url, auth)
        r = requests.get(base_url + f"/pairs?org={name}", headers=auth, timeout=T)
        assert r.status_code == 200 and r.json() == {"roundPairs": []}

    def test_pairs_missing_org(self, base_url, auth):
        r = requests.get(base_url + "/pairs", headers=auth, timeout=T)
        assert r.status_code == 400 and "must contain org" in r.text

    def test_method_not_allowed(self, base_url, auth):
        r = requests.put(base_url + "/orgs", headers=auth, timeout=T)
        assert r.status_code == 405 and r.headers["Allow"] == "GET"
        assert "Only GET requests are allowed" in r.text
