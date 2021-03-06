// Copyright 2015 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package keystone_test

import (
	"context"
	"io/ioutil"
	"net/http"
	"net/url"
	"regexp"
	"testing"

	qt "github.com/frankban/quicktest"
	"github.com/frankban/quicktest/qtsuite"
	"gopkg.in/errgo.v1"
	"gopkg.in/httprequest.v1"
	envschemaform "gopkg.in/juju/environschema.v1/form"
	"gopkg.in/macaroon-bakery.v2/httpbakery"
	"gopkg.in/macaroon-bakery.v2/httpbakery/form"

	"github.com/CanonicalLtd/candid/idp"
	"github.com/CanonicalLtd/candid/idp/keystone"
	"github.com/CanonicalLtd/candid/idp/keystone/internal/mockkeystone"
	"github.com/CanonicalLtd/candid/internal/candidtest"
	"github.com/CanonicalLtd/candid/internal/discharger"
	"github.com/CanonicalLtd/candid/internal/identity"
)

type dischargeSuite struct {
	candid           *candidtest.Server
	dischargeCreator *candidtest.DischargeCreator
	server           *mockkeystone.Server
	params           keystone.Params
}

func TestDischarge(t *testing.T) {
	qtsuite.Run(qt.New(t), &dischargeSuite{})
}

func (s *dischargeSuite) Init(c *qt.C) {
	candidtest.LogTo(c)

	s.server = mockkeystone.NewServer()
	c.Defer(s.server.Close)
	s.params = keystone.Params{
		Name:        "openstack",
		Description: "OpenStack",
		Domain:      "openstack",
		URL:         s.server.URL,
	}
	s.server.TokensFunc = testTokens
	s.server.TenantsFunc = testTenants

	store := candidtest.NewStore()
	sp := store.ServerParams()
	sp.IdentityProviders = []idp.IdentityProvider{
		keystone.NewIdentityProvider(s.params),
		keystone.NewUserpassIdentityProvider(
			keystone.Params{
				Name:        "form",
				Domain:      s.params.Domain,
				Description: s.params.Description,
				URL:         s.params.URL,
			},
		),
		keystone.NewTokenIdentityProvider(
			keystone.Params{
				Name:        "token",
				Domain:      s.params.Domain,
				Description: s.params.Description,
				URL:         s.params.URL,
			},
		),
	}
	s.candid = candidtest.NewServer(c, sp, map[string]identity.NewAPIHandlerFunc{
		"discharger": discharger.NewAPIHandler,
	})
	s.dischargeCreator = candidtest.NewDischargeCreator(s.candid)

}

func (s *dischargeSuite) TestInteractiveDischarge(c *qt.C) {
	s.dischargeCreator.AssertDischarge(c, httpbakery.WebBrowserInteractor{
		OpenWebBrowser: s.visitInteractive,
	})
}

var urlRegexp = regexp.MustCompile(`[Aa][Cc][Tt][Ii][Oo][Nn]="(.*)"`)

func (s *dischargeSuite) visitInteractive(u *url.URL) error {
	resp, err := http.Get(u.String())
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	sm := urlRegexp.FindSubmatch(body)
	if len(sm) < 2 {
		return errgo.Newf("could not find URL: %q", body)
	}
	resp, err = http.PostForm(string(sm[1]), url.Values{
		"username": []string{"testuser"},
		"password": []string{"testpass"},
	})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return errgo.Newf("bad status %q", resp.Status)
	}
	return nil
}

func (s *dischargeSuite) TestFormDischarge(c *qt.C) {
	s.dischargeCreator.AssertDischarge(c, form.Interactor{
		Filler: keystoneFormFiller{
			username: "testuser",
			password: "testpass",
		},
	})
}

type keystoneFormFiller struct {
	username, password string
}

func (h keystoneFormFiller) Fill(f envschemaform.Form) (map[string]interface{}, error) {
	if _, ok := f.Fields["username"]; !ok {
		return nil, errgo.New("schema has no username")
	}
	if _, ok := f.Fields["password"]; !ok {
		return nil, errgo.New("schema has no password")
	}
	return map[string]interface{}{
		"username": h.username,
		"password": h.password,
	}, nil
}

func (s *dischargeSuite) TestTokenDischarge(c *qt.C) {
	s.dischargeCreator.AssertDischarge(c, &tokenInteractor{})
}

type tokenLoginRequest struct {
	httprequest.Route `httprequest:"POST"`
	Token             keystone.Token `httprequest:",body"`
}

type TokenInteractionInfo struct {
	URL string `json:"url"`
}

type tokenInteractor struct{}

func (i *tokenInteractor) Kind() string {
	return "token"
}

func (i *tokenInteractor) Interact(ctx context.Context, client *httpbakery.Client, location string, ierr *httpbakery.Error) (*httpbakery.DischargeToken, error) {
	var info keystone.TokenInteractionInfo
	if err := ierr.InteractionMethod("token", &info); err != nil {
		return nil, errgo.Mask(err, errgo.Is(httpbakery.ErrInteractionMethodNotFound))
	}
	var req keystone.TokenLoginRequest
	req.Token.Login.ID = "789"
	var resp keystone.TokenLoginResponse
	cl := &httprequest.Client{
		Doer: client,
	}
	if err := cl.CallURL(ctx, info.URL, &req, &resp); err != nil {
		return nil, errgo.Mask(err)
	}
	return resp.DischargeToken, nil
}
