// Copyright (c) Mainflux
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"time"

	"github.com/MainfluxLabs/mainflux"
	"github.com/MainfluxLabs/mainflux/pkg/errors"
)

const (
	recoveryDuration = 5 * time.Minute
	ViewerRole       = "viewer"
	AdminRole        = "admin"
	OwnerRole        = "owner"
	EditorRole       = "editor"
	RootSubject      = "root"
	OrgSubject       = "org"
)

var (
	// ErrFailedToRetrieveMembers failed to retrieve group members.
	ErrFailedToRetrieveMembers = errors.New("failed to retrieve org members")

	// ErrFailedToRetrieveMembership failed to retrieve memberships
	ErrFailedToRetrieveMembership = errors.New("failed to retrieve memberships")

	errIssueUser      = errors.New("failed to issue new login key")
	errIssueTmp       = errors.New("failed to issue new temporary key")
	errRevoke         = errors.New("failed to remove key")
	errRetrieve       = errors.New("failed to retrieve key data")
	errIdentify       = errors.New("failed to validate token")
	errUnknownSubject = errors.New("unknown subject")
)

type Roles interface {
	// AssignRole assigns a role to a user.
	AssignRole(ctx context.Context, id, role string) error

	// RetrieveRole retrieves a role for a user.
	RetrieveRole(ctx context.Context, id string) (string, error)
}

// Authn specifies an API that must be fullfiled by the domain service
// implementation, and all of its decorators (e.g. logging & metrics).
// Token is a string value of the actual Key and is used to authenticate
// an Auth service request.
type Authn interface {
	// Issue issues a new Key, returning its token value alongside.
	Issue(ctx context.Context, token string, key Key) (Key, string, error)

	// Revoke removes the Key with the provided id that is
	// issued by the user identified by the provided key.
	Revoke(ctx context.Context, token, id string) error

	// RetrieveKey retrieves data for the Key identified by the provided
	// ID, that is issued by the user identified by the provided key.
	RetrieveKey(ctx context.Context, token, id string) (Key, error)

	// Identify validates token token. If token is valid, content
	// is returned. If token is invalid, or invocation failed for some
	// other reason, non-nil error value is returned in response.
	Identify(ctx context.Context, token string) (Identity, error)
}

// AuthzReq represents an argument struct for making an authz related function calls.
type AuthzReq struct {
	Token   string
	Object  string
	Subject string
	Action  string
}

// Authz represents a authorization service. It exposes
// functionalities through `auth` to perform authorization.
type Authz interface {
	Authorize(ctx context.Context, ar AuthzReq) error
}

// Service specifies an API that must be fulfilled by the domain service
// implementation, and all of its decorators (e.g. logging & metrics).
// Token is a string value of the actual Key and is used to authenticate
// an Auth service request.
type Service interface {
	Authn
	Authz
	Roles
	Orgs
}

var _ Service = (*service)(nil)

type service struct {
	orgs          OrgRepository
	users         mainflux.UsersServiceClient
	things        mainflux.ThingsServiceClient
	keys          KeyRepository
	roles         RolesRepository
	idProvider    mainflux.IDProvider
	tokenizer     Tokenizer
	loginDuration time.Duration
}

// New instantiates the auth service implementation.
func New(orgs OrgRepository, tc mainflux.ThingsServiceClient, uc mainflux.UsersServiceClient, keys KeyRepository, roles RolesRepository, idp mainflux.IDProvider, tokenizer Tokenizer, duration time.Duration) Service {
	return &service{
		tokenizer:     tokenizer,
		things:        tc,
		orgs:          orgs,
		users:         uc,
		keys:          keys,
		roles:         roles,
		idProvider:    idp,
		loginDuration: duration,
	}
}

func (svc service) Issue(ctx context.Context, token string, key Key) (Key, string, error) {
	if key.IssuedAt.IsZero() {
		return Key{}, "", ErrInvalidKeyIssuedAt
	}
	switch key.Type {
	case APIKey:
		return svc.userKey(ctx, token, key)
	case RecoveryKey:
		return svc.tmpKey(recoveryDuration, key)
	default:
		return svc.tmpKey(svc.loginDuration, key)
	}
}

func (svc service) Revoke(ctx context.Context, token, id string) error {
	issuerID, _, err := svc.login(token)
	if err != nil {
		return errors.Wrap(errRevoke, err)
	}
	if err := svc.keys.Remove(ctx, issuerID, id); err != nil {
		return errors.Wrap(errRevoke, err)
	}
	return nil
}

func (svc service) RetrieveKey(ctx context.Context, token, id string) (Key, error) {
	issuerID, _, err := svc.login(token)
	if err != nil {
		return Key{}, errors.Wrap(errRetrieve, err)
	}

	return svc.keys.Retrieve(ctx, issuerID, id)
}

func (svc service) Identify(ctx context.Context, token string) (Identity, error) {
	return svc.identify(ctx, token)
}

func (svc service) Authorize(ctx context.Context, ar AuthzReq) error {
	switch ar.Subject {
	case RootSubject:
		return svc.isAdmin(ctx, ar.Token)
	default:
		return errUnknownSubject
	}
}

func (svc service) tmpKey(duration time.Duration, key Key) (Key, string, error) {
	key.ExpiresAt = key.IssuedAt.Add(duration)
	secret, err := svc.tokenizer.Issue(key)
	if err != nil {
		return Key{}, "", errors.Wrap(errIssueTmp, err)
	}

	return key, secret, nil
}

func (svc service) userKey(ctx context.Context, token string, key Key) (Key, string, error) {
	id, sub, err := svc.login(token)
	if err != nil {
		return Key{}, "", errors.Wrap(errIssueUser, err)
	}

	key.IssuerID = id
	if key.Subject == "" {
		key.Subject = sub
	}

	keyID, err := svc.idProvider.ID()
	if err != nil {
		return Key{}, "", errors.Wrap(errIssueUser, err)
	}
	key.ID = keyID

	if _, err := svc.keys.Save(ctx, key); err != nil {
		return Key{}, "", errors.Wrap(errIssueUser, err)
	}

	secret, err := svc.tokenizer.Issue(key)
	if err != nil {
		return Key{}, "", errors.Wrap(errIssueUser, err)
	}

	return key, secret, nil
}

func (svc service) login(token string) (string, string, error) {
	key, err := svc.tokenizer.Parse(token)
	if err != nil {
		return "", "", err
	}
	// Only login key token is valid for login.
	if key.Type != LoginKey || key.IssuerID == "" {
		return "", "", errors.ErrAuthentication
	}

	return key.IssuerID, key.Subject, nil
}

func getTimestmap() time.Time {
	return time.Now().UTC().Round(time.Millisecond)
}

func (svc service) CreateOrg(ctx context.Context, token string, o Org) (Org, error) {
	user, err := svc.Identify(ctx, token)
	if err != nil {
		return Org{}, err
	}

	id, err := svc.idProvider.ID()
	if err != nil {
		return Org{}, err
	}

	timestamp := getTimestmap()

	org := Org{
		ID:          id,
		OwnerID:     user.ID,
		Name:        o.Name,
		Description: o.Description,
		Metadata:    o.Metadata,
		UpdatedAt:   timestamp,
		CreatedAt:   timestamp,
	}

	if err := svc.orgs.Save(ctx, org); err != nil {
		return Org{}, err
	}

	om := OrgMember{
		OrgID:     id,
		MemberID:  user.ID,
		Role:      OwnerRole,
		CreatedAt: timestamp,
		UpdatedAt: timestamp,
	}

	if err := svc.orgs.AssignMembers(ctx, om); err != nil {
		return Org{}, err
	}

	return org, nil
}

func (svc service) ListOrgs(ctx context.Context, token string, pm PageMetadata) (OrgsPage, error) {
	if err := svc.isAdmin(ctx, token); err == nil {
		return svc.orgs.RetrieveByAdmin(ctx, pm)
	}

	user, err := svc.Identify(ctx, token)
	if err != nil {
		return OrgsPage{}, err
	}

	return svc.orgs.RetrieveByOwner(ctx, user.ID, pm)
}

func (svc service) RemoveOrg(ctx context.Context, token, id string) error {
	user, err := svc.Identify(ctx, token)
	if err != nil {
		return err
	}

	if err := svc.orgRolesAuth(ctx, token, id, OwnerRole); err != nil {
		return err
	}

	return svc.orgs.Delete(ctx, user.ID, id)
}

func (svc service) UpdateOrg(ctx context.Context, token string, o Org) (Org, error) {
	user, err := svc.Identify(ctx, token)
	if err != nil {
		return Org{}, err
	}

	if err := svc.orgRolesAuth(ctx, token, o.ID, AdminRole); err != nil {
		return Org{}, err
	}

	org := Org{
		ID:          o.ID,
		OwnerID:     user.ID,
		Name:        o.Name,
		Description: o.Description,
		Metadata:    o.Metadata,
		UpdatedAt:   getTimestmap(),
	}

	if err := svc.orgs.Update(ctx, org); err != nil {
		return Org{}, err
	}

	return org, nil
}

func (svc service) ViewOrg(ctx context.Context, token, id string) (Org, error) {
	if err := svc.orgRolesAuth(ctx, token, id, ViewerRole); err != nil {
		return Org{}, err
	}

	org, err := svc.orgs.RetrieveByID(ctx, id)
	if err != nil {
		return Org{}, err
	}

	return org, nil
}

func (svc service) AssignMembers(ctx context.Context, token, orgID string, oms ...OrgMember) error {
	if err := svc.orgRolesAuth(ctx, token, orgID, AdminRole); err != nil {
		return err
	}

	var memberEmails []string
	var roleByEmail = make(map[string]string)
	for _, om := range oms {
		roleByEmail[om.Email] = om.Role
		memberEmails = append(memberEmails, om.Email)
	}

	muReq := mainflux.UsersByEmailsReq{Emails: memberEmails}
	usr, err := svc.users.GetUsersByEmails(ctx, &muReq)
	if err != nil {
		return err
	}

	timestamp := getTimestmap()
	var members []OrgMember
	for _, user := range usr.Users {
		member := OrgMember{
			OrgID:     orgID,
			MemberID:  user.Id,
			Role:      roleByEmail[user.Email],
			UpdatedAt: timestamp,
			CreatedAt: timestamp,
		}

		members = append(members, member)
	}

	if err := svc.orgs.AssignMembers(ctx, members...); err != nil {
		return err
	}

	return nil
}

func (svc service) UnassignMembers(ctx context.Context, token string, orgID string, memberIDs ...string) error {
	if err := svc.canAssignMembers(ctx, token, orgID, memberIDs...); err != nil {
		return err
	}

	/*grs, err := svc.orgs.RetrieveGroups(ctx, orgID, PageMetadata{})
	if err != nil {
		return err
	}

	for _, gr := range grs.OrgGroups {
		if err := svc.policies.RemoveGroupPolicies(ctx, gr.GroupID, memberIDs...); err != nil {
			return err
		}
	}*/

	if err := svc.orgs.UnassignMembers(ctx, orgID, memberIDs...); err != nil {
		return err
	}

	return nil
}

func (svc service) ViewMember(ctx context.Context, token, orgID, memberID string) (OrgMember, error) {
	if err := svc.orgRolesAuth(ctx, token, orgID, ViewerRole); err != nil {
		return OrgMember{}, err
	}

	usrReq := mainflux.UsersByIDsReq{Ids: []string{memberID}}
	page, err := svc.users.GetUsersByIDs(ctx, &usrReq)
	if err != nil {
		return OrgMember{}, err
	}

	role, err := svc.orgs.RetrieveRole(ctx, memberID, orgID)
	if err != nil {
		return OrgMember{}, err
	}

	member := OrgMember{
		MemberID: page.Users[0].Id,
		Email:    page.Users[0].Email,
		Role:     role,
	}

	return member, nil
}

func (svc service) UpdateMembers(ctx context.Context, token, orgID string, members ...OrgMember) error {
	if err := svc.orgRolesAuth(ctx, token, orgID, AdminRole); err != nil {
		return err
	}

	org, err := svc.orgs.RetrieveByID(ctx, orgID)
	if err != nil {
		return err
	}

	var memberEmails []string
	var roleByEmail = make(map[string]string)
	for _, m := range members {
		roleByEmail[m.Email] = m.Role
		memberEmails = append(memberEmails, m.Email)
	}

	muReq := mainflux.UsersByEmailsReq{Emails: memberEmails}
	usr, err := svc.users.GetUsersByEmails(ctx, &muReq)
	if err != nil {
		return err
	}

	var oms []OrgMember
	for _, user := range usr.Users {
		if user.Id == org.OwnerID {
			return errors.ErrAuthorization
		}

		om := OrgMember{
			OrgID:     orgID,
			MemberID:  user.Id,
			Role:      roleByEmail[user.Email],
			UpdatedAt: getTimestmap(),
		}

		oms = append(oms, om)
	}

	if err := svc.orgs.UpdateMembers(ctx, oms...); err != nil {
		return err
	}

	return nil
}

func (svc service) ListOrgMembers(ctx context.Context, token string, orgID string, pm PageMetadata) (OrgMembersPage, error) {
	if err := svc.orgRolesAuth(ctx, token, orgID, ViewerRole); err != nil {
		return OrgMembersPage{}, err
	}

	omp, err := svc.orgs.RetrieveMembers(ctx, orgID, pm)
	if err != nil {
		return OrgMembersPage{}, errors.Wrap(ErrFailedToRetrieveMembers, err)
	}

	var oms []OrgMember
	if len(omp.OrgMembers) > 0 {
		var memberIDs []string
		var roleByEmail = make(map[string]string)
		for _, m := range omp.OrgMembers {
			roleByEmail[m.MemberID] = m.Role
			memberIDs = append(memberIDs, m.MemberID)
		}

		usrReq := mainflux.UsersByIDsReq{Ids: memberIDs}
		page, err := svc.users.GetUsersByIDs(ctx, &usrReq)
		if err != nil {
			return OrgMembersPage{}, err
		}

		for _, user := range page.Users {
			mbr := OrgMember{
				MemberID: user.Id,
				Email:    user.Email,
				Role:     roleByEmail[user.Id],
			}
			oms = append(oms, mbr)
		}
	}

	mpg := OrgMembersPage{
		OrgMembers: oms,
		PageMetadata: PageMetadata{
			Total:  omp.Total,
			Offset: omp.Offset,
			Limit:  omp.Limit,
		},
	}

	return mpg, nil
}

func (svc service) ListOrgMemberships(ctx context.Context, token string, memberID string, pm PageMetadata) (OrgsPage, error) {
	if err := svc.isAdmin(ctx, token); err == nil {
		return svc.orgs.RetrieveMemberships(ctx, memberID, pm)
	}

	user, err := svc.Identify(ctx, token)
	if err != nil {
		return OrgsPage{}, err
	}

	if user.ID != memberID {
		return OrgsPage{}, errors.ErrAuthorization
	}

	return svc.orgs.RetrieveMemberships(ctx, memberID, pm)
}

func (svc service) Backup(ctx context.Context, token string) (Backup, error) {
	if err := svc.isAdmin(ctx, token); err != nil {
		return Backup{}, err
	}

	orgs, err := svc.orgs.RetrieveAll(ctx)
	if err != nil {
		return Backup{}, err
	}

	mrs, err := svc.orgs.RetrieveAllOrgMembers(ctx)
	if err != nil {
		return Backup{}, err
	}

	ogs, err := svc.orgs.RetrieveAllOrgGroups(ctx)
	if err != nil {
		return Backup{}, err
	}

	backup := Backup{
		Orgs:       orgs,
		OrgMembers: mrs,
		OrgGroups:  ogs,
	}

	return backup, nil
}

func (svc service) Restore(ctx context.Context, token string, backup Backup) error {
	if err := svc.isAdmin(ctx, token); err != nil {
		return err
	}

	if err := svc.orgs.Save(ctx, backup.Orgs...); err != nil {
		return err
	}

	if err := svc.orgs.AssignMembers(ctx, backup.OrgMembers...); err != nil {
		return err
	}

	return nil
}

func (svc service) AssignRole(ctx context.Context, id, role string) error {
	return svc.roles.SaveRole(ctx, id, role)
}

func (svc service) RetrieveRole(ctx context.Context, id string) (string, error) {
	return svc.roles.RetrieveRole(ctx, id)
}

func (svc service) isAdmin(ctx context.Context, token string) error {
	user, err := svc.identify(ctx, token)
	if err != nil {
		return err
	}

	role, err := svc.roles.RetrieveRole(ctx, user.ID)
	if err != nil {
		return err
	}

	if role != RoleAdmin && role != RoleRootAdmin {
		return errors.ErrAuthorization
	}

	return nil
}

func (svc service) orgRolesAuth(ctx context.Context, token, orgID string, action string) error {
	if err := svc.isAdmin(ctx, token); err == nil {
		return nil
	}

	user, err := svc.Identify(ctx, token)
	if err != nil {
		return err
	}

	role, err := svc.orgs.RetrieveRole(ctx, user.ID, orgID)
	if err != nil {
		return err
	}

	switch role {
	case OwnerRole:
		return nil
	case AdminRole:
		if action == ViewerRole || action == EditorRole || action == AdminRole {
			return nil
		}
	case EditorRole:
		if action == ViewerRole || action == EditorRole {
			return nil
		}
	case ViewerRole:
		if action == ViewerRole {
			return nil
		}
	}

	return errors.ErrAuthorization
}

func (svc service) canAssignMembers(ctx context.Context, token, orgID string, memberIDs ...string) error {
	if err := svc.orgRolesAuth(ctx, token, orgID, AdminRole); err != nil {
		return err
	}

	for _, memberID := range memberIDs {
		role, err := svc.orgs.RetrieveRole(ctx, memberID, orgID)
		if err != nil {
			return err
		}

		if role == OwnerRole {
			return errors.ErrAuthorization
		}
	}

	return nil
}

func (svc service) identify(ctx context.Context, token string) (Identity, error) {
	key, err := svc.tokenizer.Parse(token)
	if err == ErrAPIKeyExpired {
		err = svc.keys.Remove(ctx, key.IssuerID, key.ID)
		return Identity{}, errors.Wrap(ErrAPIKeyExpired, err)
	}
	if err != nil {
		return Identity{}, errors.Wrap(errIdentify, err)
	}

	switch key.Type {
	case RecoveryKey, LoginKey:
		return Identity{ID: key.IssuerID, Email: key.Subject}, nil
	case APIKey:
		_, err := svc.keys.Retrieve(context.TODO(), key.IssuerID, key.ID)
		if err != nil {
			return Identity{}, errors.ErrAuthentication
		}
		return Identity{ID: key.IssuerID, Email: key.Subject}, nil
	default:
		return Identity{}, errors.ErrAuthentication
	}
}
