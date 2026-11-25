import requests
T = 10


def add(app, auth, org, date):
    return requests.post(app + "/round", params={"org": org, "round": date}, headers=auth, timeout=T)


def listing(app, auth, org):
    return requests.get(app + "/rounds", params={"org": org}, headers=auth, timeout=T)


class TestRoundsM2:
    def test_add_and_list_ordered(self, app, auth, org):
        n = org[0]
        assert add(app, auth, n, "2030-03-01T10:00:00Z").status_code == 200
        assert add(app, auth, n, "2030-04-01T10:00:00Z").status_code == 200
        r = listing(app, auth, n)
        assert r.status_code == 200
        assert r.json()["rounds"] == ["2030-03-01 10:00:00Z", "2030-04-01 10:00:00Z"]

    def test_list_empty_org(self, app, auth, org):
        r = listing(app, auth, org[0])
        assert r.status_code == 200 and r.json() == {"rounds": []}

    def test_delete_round(self, app, auth, org):
        n = org[0]
        add(app, auth, n, "2030-03-01T10:00:00Z")
        add(app, auth, n, "2030-04-01T10:00:00Z")
        r = requests.delete(app + "/round", params={"org": n, "roundId": "1"}, headers=auth, timeout=T)
        assert r.status_code == 200
        assert listing(app, auth, n).json()["rounds"] == ["2030-03-01 10:00:00Z"]

    def test_reschedule_round(self, app, auth, org):
        n = org[0]
        add(app, auth, n, "2030-03-01T10:00:00Z")
        r = requests.post(app + "/round", params={"org": n, "round": "2031-05-05T09:00:00Z", "roundId": "0"}, headers=auth, timeout=T)
        assert r.status_code == 200
        assert listing(app, auth, n).json()["rounds"] == ["2031-05-05 09:00:00Z"]

    def test_delete_missing_round_id(self, app, auth, org):
        r = requests.delete(app + "/round", params={"org": org[0]}, headers=auth, timeout=T)
        assert r.status_code == 400

    def test_delete_non_numeric_round_id(self, app, auth, org):
        r = requests.delete(app + "/round", params={"org": org[0], "roundId": "abc"}, headers=auth, timeout=T)
        assert r.status_code == 500

    def test_add_missing_org(self, app, auth):
        r = requests.post(app + "/round", params={"round": "2030-03-01T10:00:00Z"}, headers=auth, timeout=T)
        assert r.status_code == 400

    def test_add_missing_round(self, app, auth, org):
        r = requests.post(app + "/round", params={"org": org[0]}, headers=auth, timeout=T)
        assert r.status_code == 400

    def test_rounds_wrong_method(self, app, auth):
        assert requests.post(app + "/rounds", params={"org": "x"}, headers=auth, timeout=T).status_code == 405

    def test_rounds_requires_auth(self, app, auth, org):
        r = requests.get(app + "/rounds", params={"org": org[0]}, timeout=T)
        assert r.text == ""  # handler not executed (auth middleware writes no body)

    def test_round_requires_auth(self, app, auth, org):
        requests.post(app + "/round", params={"org": org[0], "round": "2030-01-01T00:00:00Z"}, timeout=T)
        assert listing(app, auth, org[0]).json() == {"rounds": []}
