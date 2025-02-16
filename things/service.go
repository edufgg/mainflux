// Copyright (c) Mainflux
// SPDX-License-Identifier: Apache-2.0

package things

import (
	"context"
	"encoding/json"
	"time"

	"github.com/MainfluxLabs/mainflux"
	"github.com/MainfluxLabs/mainflux/auth"
	"github.com/MainfluxLabs/mainflux/pkg/errors"
)

const (
	Read      = "r"
	ReadWrite = "r_w"
)

// Service specifies an API that must be fullfiled by the domain service
// implementation, and all of its decorators (e.g. logging & metrics).
type Service interface {
	// CreateThings adds things to the user identified by the provided key.
	CreateThings(ctx context.Context, token string, things ...Thing) ([]Thing, error)

	// UpdateThing updates the thing identified by the provided ID, that
	// belongs to the user identified by the provided key.
	UpdateThing(ctx context.Context, token string, thing Thing) error

	// UpdateKey updates key value of the existing thing. A non-nil error is
	// returned to indicate operation failure.
	UpdateKey(ctx context.Context, token, id, key string) error

	// ViewThing retrieves data about the thing identified with the provided
	// ID, that belongs to the user identified by the provided key.
	ViewThing(ctx context.Context, token, id string) (Thing, error)

	// ListThings retrieves data about subset of things that belongs to the
	// user identified by the provided key.
	ListThings(ctx context.Context, token string, pm PageMetadata) (ThingsPage, error)

	// ListThingsByIDs retrieves data about subset of things that are identified
	ListThingsByIDs(ctx context.Context, ids []string) (ThingsPage, error)

	// ListThingsByChannel retrieves data about subset of things that are
	// connected or not connected to specified channel and belong to the user identified by
	// the provided key.
	ListThingsByChannel(ctx context.Context, token, chID string, pm PageMetadata) (ThingsPage, error)

	// RemoveThings removes the things identified with the provided IDs, that
	// belongs to the user identified by the provided key.
	RemoveThings(ctx context.Context, token string, id ...string) error

	// CreateChannels adds channels to the user identified by the provided key.
	CreateChannels(ctx context.Context, token string, channels ...Channel) ([]Channel, error)

	// UpdateChannel updates the channel identified by the provided ID, that
	// belongs to the user identified by the provided key.
	UpdateChannel(ctx context.Context, token string, channel Channel) error

	// ViewChannel retrieves data about the channel identified by the provided
	// ID, that belongs to the user identified by the provided key.
	ViewChannel(ctx context.Context, token, id string) (Channel, error)

	// ListChannels retrieves data about subset of channels that belongs to the
	// user identified by the provided key.
	ListChannels(ctx context.Context, token string, pm PageMetadata) (ChannelsPage, error)

	// ViewChannelByThing retrieves data about channel that have
	// specified thing connected or not connected to it and belong to the user identified by
	// the provided key.
	ViewChannelByThing(ctx context.Context, token, thID string) (Channel, error)

	// RemoveChannels removes the things identified by the provided IDs, that
	// belongs to the user identified by the provided key.
	RemoveChannels(ctx context.Context, token string, ids ...string) error

	// ViewChannelProfile retrieves channel profile.
	ViewChannelProfile(ctx context.Context, chID string) (Profile, error)

	// Connect connects a list of things to a channel.
	Connect(ctx context.Context, token, chID string, thIDs []string) error

	// Disconnect disconnects a list of things from a channel.
	Disconnect(ctx context.Context, token, chID string, thIDs []string) error

	// GetConnByKey determines whether the channel can be accessed using the
	// provided key and returns thing's id if access is allowed.
	GetConnByKey(ctx context.Context, key string) (Connection, error)

	// IsChannelOwner determines whether the channel can be accessed by
	// the given user and returns error if it cannot.
	IsChannelOwner(ctx context.Context, owner, chanID string) error

	// IsThingOwner determines whether the thing can be accessed by
	// the given user and returns error if it cannot.
	IsThingOwner(ctx context.Context, token, thingID string) error

	// Identify returns thing ID for given thing key.
	Identify(ctx context.Context, key string) (string, error)

	// Backup retrieves all things, channels and connections for all users. Only accessible by admin.
	Backup(ctx context.Context, token string) (Backup, error)

	// Restore adds things, channels and connections from a backup. Only accessible by admin.
	Restore(ctx context.Context, token string, backup Backup) error

	Groups

	Policies
}

// PageMetadata contains page metadata that helps navigation.
type PageMetadata struct {
	Total        uint64
	Offset       uint64                 `json:"offset,omitempty"`
	Limit        uint64                 `json:"limit,omitempty"`
	Name         string                 `json:"name,omitempty"`
	Order        string                 `json:"order,omitempty"`
	Dir          string                 `json:"dir,omitempty"`
	Metadata     map[string]interface{} `json:"metadata,omitempty"`
	Disconnected bool                   // Used for connected or disconnected lists
}

type Backup struct {
	Things        []Thing
	Channels      []Channel
	Connections   []Connection
	Groups        []Group
	GroupPolicies []GroupPolicy
}

var _ Service = (*thingsService)(nil)

type thingsService struct {
	auth         mainflux.AuthServiceClient
	users        mainflux.UsersServiceClient
	things       ThingRepository
	channels     ChannelRepository
	groups       GroupRepository
	policies     PoliciesRepository
	channelCache ChannelCache
	thingCache   ThingCache
	idProvider   mainflux.IDProvider
}

// New instantiates the things service implementation.
func New(auth mainflux.AuthServiceClient, users mainflux.UsersServiceClient, things ThingRepository, channels ChannelRepository, groups GroupRepository, policies PoliciesRepository, ccache ChannelCache, tcache ThingCache, idp mainflux.IDProvider) Service {
	return &thingsService{
		auth:         auth,
		users:        users,
		things:       things,
		channels:     channels,
		groups:       groups,
		policies:     policies,
		channelCache: ccache,
		thingCache:   tcache,
		idProvider:   idp,
	}
}

func (ts *thingsService) CreateThings(ctx context.Context, token string, things ...Thing) ([]Thing, error) {
	res, err := ts.auth.Identify(ctx, &mainflux.Token{Value: token})
	if err != nil {
		return []Thing{}, err
	}

	ths := []Thing{}
	for _, thing := range things {
		th, err := ts.createThing(ctx, &thing, res)

		if err != nil {
			return []Thing{}, err
		}
		ths = append(ths, th)
	}

	return ths, nil
}

func (ts *thingsService) createThing(ctx context.Context, thing *Thing, identity *mainflux.UserIdentity) (Thing, error) {
	thing.OwnerID = identity.GetId()

	if thing.ID == "" {
		id, err := ts.idProvider.ID()
		if err != nil {
			return Thing{}, err
		}
		thing.ID = id
	}

	if thing.Key == "" {
		key, err := ts.idProvider.ID()

		if err != nil {
			return Thing{}, err
		}
		thing.Key = key
	}

	ths, err := ts.things.Save(ctx, *thing)
	if err != nil {
		return Thing{}, err
	}
	if len(ths) == 0 {
		return Thing{}, errors.ErrCreateEntity
	}
	return ths[0], nil
}

func (ts *thingsService) UpdateThing(ctx context.Context, token string, thing Thing) error {
	res, err := ts.auth.Identify(ctx, &mainflux.Token{Value: token})
	if err != nil {
		return err
	}

	if err := ts.IsThingOwner(ctx, token, thing.ID); err != nil {
		return err
	}

	thing.OwnerID = res.GetId()

	return ts.things.Update(ctx, thing)
}

func (ts *thingsService) UpdateKey(ctx context.Context, token, id, key string) error {
	res, err := ts.auth.Identify(ctx, &mainflux.Token{Value: token})
	if err != nil {
		return errors.Wrap(errors.ErrAuthentication, err)
	}

	owner := res.GetId()

	return ts.things.UpdateKey(ctx, owner, id, key)
}

func (ts *thingsService) ViewThing(ctx context.Context, token, id string) (Thing, error) {
	thing, err := ts.things.RetrieveByID(ctx, id)
	if err != nil {
		return Thing{}, err
	}

	if err := ts.authorize(ctx, auth.RootSubject, token); err == nil {
		return thing, nil
	}

	res, err := ts.auth.Identify(ctx, &mainflux.Token{Value: token})
	if err != nil {
		return Thing{}, errors.Wrap(errors.ErrAuthentication, err)
	}

	if thing.OwnerID == res.GetId() {
		return thing, nil
	}

	gp := GroupPolicy{
		MemberID: res.GetId(),
		GroupID:  thing.GroupID,
	}
	p, err := ts.policies.RetrieveGroupPolicy(ctx, gp)
	if err != nil {
		return Thing{}, err
	}
	if p != Read && p != ReadWrite {
		return Thing{}, errors.ErrAuthorization
	}

	return thing, nil
}

func (ts *thingsService) ListThings(ctx context.Context, token string, pm PageMetadata) (ThingsPage, error) {
	if err := ts.authorize(ctx, auth.RootSubject, token); err == nil {
		return ts.things.RetrieveByAdmin(ctx, pm)
	}

	res, err := ts.auth.Identify(ctx, &mainflux.Token{Value: token})
	if err != nil {
		return ThingsPage{}, errors.Wrap(errors.ErrAuthentication, err)
	}

	return ts.things.RetrieveByOwner(ctx, res.GetId(), pm)
}

func (ts *thingsService) ListThingsByIDs(ctx context.Context, ids []string) (ThingsPage, error) {
	things, err := ts.things.RetrieveByIDs(ctx, ids, PageMetadata{})
	if err != nil {
		return ThingsPage{}, err
	}
	return things, nil
}

func (ts *thingsService) ListThingsByChannel(ctx context.Context, token, chID string, pm PageMetadata) (ThingsPage, error) {
	res, err := ts.auth.Identify(ctx, &mainflux.Token{Value: token})
	if err != nil {
		return ThingsPage{}, errors.Wrap(errors.ErrAuthentication, err)
	}

	return ts.things.RetrieveByChannel(ctx, res.GetId(), chID, pm)
}

func (ts *thingsService) RemoveThings(ctx context.Context, token string, ids ...string) error {
	res, err := ts.auth.Identify(ctx, &mainflux.Token{Value: token})
	if err != nil {
		return errors.Wrap(errors.ErrAuthentication, err)
	}

	for _, id := range ids {
		if err := ts.thingCache.Remove(ctx, id); err != nil {
			return err
		}
	}

	if err := ts.things.Remove(ctx, res.GetId(), ids...); err != nil {
		return err
	}

	return nil
}

func (ts *thingsService) CreateChannels(ctx context.Context, token string, channels ...Channel) ([]Channel, error) {
	res, err := ts.auth.Identify(ctx, &mainflux.Token{Value: token})
	if err != nil {
		return []Channel{}, errors.Wrap(errors.ErrAuthentication, err)
	}

	chs := []Channel{}
	for _, channel := range channels {
		ch, err := ts.createChannel(ctx, &channel, res)
		if err != nil {
			return []Channel{}, err
		}
		chs = append(chs, ch)
	}
	return chs, nil
}

func (ts *thingsService) createChannel(ctx context.Context, channel *Channel, identity *mainflux.UserIdentity) (Channel, error) {
	if channel.ID == "" {
		chID, err := ts.idProvider.ID()
		if err != nil {
			return Channel{}, err
		}
		channel.ID = chID
	}
	channel.OwnerID = identity.GetId()

	chs, err := ts.channels.Save(ctx, *channel)
	if err != nil {
		return Channel{}, err
	}
	if len(chs) == 0 {
		return Channel{}, errors.ErrCreateEntity
	}

	return chs[0], nil
}

func (ts *thingsService) UpdateChannel(ctx context.Context, token string, channel Channel) error {
	res, err := ts.auth.Identify(ctx, &mainflux.Token{Value: token})
	if err != nil {
		return errors.Wrap(errors.ErrAuthentication, err)
	}

	channel.OwnerID = res.GetId()
	return ts.channels.Update(ctx, channel)
}

func (ts *thingsService) ViewChannel(ctx context.Context, token, id string) (Channel, error) {
	res, err := ts.auth.Identify(ctx, &mainflux.Token{Value: token})
	if err != nil {
		return Channel{}, errors.Wrap(errors.ErrAuthentication, err)
	}

	channel, err := ts.channels.RetrieveByID(ctx, id)
	if err != nil {
		return Channel{}, err
	}

	if err := ts.authorize(ctx, auth.RootSubject, token); err == nil {
		return channel, nil
	}

	if channel.OwnerID != res.GetId() {
		return Channel{}, errors.ErrAuthorization
	}

	return channel, nil
}

func (ts *thingsService) ListChannels(ctx context.Context, token string, pm PageMetadata) (ChannelsPage, error) {
	if err := ts.authorize(ctx, auth.RootSubject, token); err == nil {
		return ts.channels.RetrieveByAdmin(ctx, pm)
	}

	res, err := ts.auth.Identify(ctx, &mainflux.Token{Value: token})
	if err != nil {
		return ChannelsPage{}, errors.Wrap(errors.ErrAuthentication, err)
	}

	return ts.channels.RetrieveByOwner(ctx, res.GetId(), pm)
}

func (ts *thingsService) ViewChannelByThing(ctx context.Context, token, thID string) (Channel, error) {
	res, err := ts.auth.Identify(ctx, &mainflux.Token{Value: token})
	if err != nil {
		return Channel{}, errors.Wrap(errors.ErrAuthentication, err)
	}

	thing, err := ts.things.RetrieveByID(ctx, thID)
	if err != nil {
		return Channel{}, err
	}

	if err := ts.authorize(ctx, auth.RootSubject, token); err == nil {
		return ts.channels.RetrieveByThing(ctx, res.GetId(), thID)
	}

	if thing.OwnerID == res.GetId() {
		return ts.channels.RetrieveByThing(ctx, res.GetId(), thID)
	}

	return ts.channels.RetrieveByThing(ctx, res.GetId(), thID)
}

func (ts *thingsService) RemoveChannels(ctx context.Context, token string, ids ...string) error {
	res, err := ts.auth.Identify(ctx, &mainflux.Token{Value: token})
	if err != nil {
		return errors.Wrap(errors.ErrAuthentication, err)
	}

	for _, id := range ids {
		if err := ts.channelCache.Remove(ctx, id); err != nil {
			return err
		}
	}

	return ts.channels.Remove(ctx, res.GetId(), ids...)
}

func (ts *thingsService) ViewChannelProfile(ctx context.Context, chID string) (Profile, error) {
	channel, err := ts.channels.RetrieveByID(ctx, chID)
	if err != nil {
		return Profile{}, err
	}

	meta, err := json.Marshal(channel.Profile)
	if err != nil {
		return Profile{}, err
	}

	var profile Profile
	if err := json.Unmarshal(meta, &profile); err != nil {
		return Profile{}, err
	}

	return profile, nil
}

func (ts *thingsService) Connect(ctx context.Context, token, chID string, thIDs []string) error {
	for _, thID := range thIDs {
		if err := ts.IsThingOwner(ctx, token, thID); err != nil {
			return err
		}
	}

	if err := ts.IsChannelOwner(ctx, token, chID); err != nil {
		return err
	}

	ch, err := ts.channels.RetrieveByID(ctx, chID)
	if err != nil {
		return err
	}

	for _, thID := range thIDs {
		th, err := ts.things.RetrieveByID(ctx, thID)
		if err != nil {
			return err
		}

		if th.GroupID != ch.GroupID {
			return errors.ErrAuthorization
		}
	}

	return ts.channels.Connect(ctx, chID, thIDs)
}

func (ts *thingsService) Disconnect(ctx context.Context, token, chID string, thIDs []string) error {
	for _, thID := range thIDs {
		if err := ts.IsThingOwner(ctx, token, thID); err != nil {
			return err
		}
	}

	if err := ts.IsChannelOwner(ctx, token, chID); err != nil {
		return err
	}

	for _, thID := range thIDs {
		if err := ts.channelCache.Disconnect(ctx, chID, thID); err != nil {
			return err
		}
	}

	return ts.channels.Disconnect(ctx, chID, thIDs)
}

func (ts *thingsService) GetConnByKey(ctx context.Context, thingKey string) (Connection, error) {
	conn, err := ts.channels.RetrieveConnByThingKey(ctx, thingKey)
	if err != nil {
		return Connection{}, err
	}

	if err := ts.thingCache.Save(ctx, thingKey, conn.ThingID); err != nil {
		return Connection{}, err
	}
	if err := ts.channelCache.Connect(ctx, conn.ChannelID, conn.ThingID); err != nil {
		return Connection{}, err
	}
	return Connection{ThingID: conn.ThingID, ChannelID: conn.ChannelID}, nil
}

func (ts *thingsService) IsChannelOwner(ctx context.Context, token, chanID string) error {
	user, err := ts.auth.Identify(ctx, &mainflux.Token{Value: token})
	if err != nil {
		return err
	}

	ch, err := ts.channels.RetrieveByID(ctx, chanID)
	if err != nil {
		return err
	}

	if ch.OwnerID != user.GetId() {
		return errors.ErrAuthorization
	}

	return nil
}

func (ts *thingsService) IsThingOwner(ctx context.Context, token, thingID string) error {
	user, err := ts.auth.Identify(ctx, &mainflux.Token{Value: token})
	if err != nil {
		return err
	}

	th, err := ts.things.RetrieveByID(ctx, thingID)
	if err != nil {
		return err
	}

	if th.OwnerID != user.GetId() {
		return errors.ErrAuthorization
	}

	return nil
}

func (ts *thingsService) Identify(ctx context.Context, key string) (string, error) {
	id, err := ts.thingCache.ID(ctx, key)
	if err == nil {
		return id, nil
	}

	id, err = ts.things.RetrieveByKey(ctx, key)
	if err != nil {
		return "", err
	}

	if err := ts.thingCache.Save(ctx, key, id); err != nil {
		return "", err
	}
	return id, nil
}

func (ts *thingsService) Backup(ctx context.Context, token string) (Backup, error) {
	if err := ts.authorize(ctx, auth.RootSubject, token); err != nil {
		return Backup{}, err
	}

	groups, err := ts.groups.RetrieveAll(ctx)
	if err != nil {
		return Backup{}, err
	}

	groupsPolicies, err := ts.policies.RetrieveAllGroupPolicies(ctx)
	if err != nil {
		return Backup{}, err
	}

	things, err := ts.things.RetrieveAll(ctx)
	if err != nil {
		return Backup{}, err
	}

	channels, err := ts.channels.RetrieveAll(ctx)
	if err != nil {
		return Backup{}, err
	}

	connections, err := ts.channels.RetrieveAllConnections(ctx)
	if err != nil {
		return Backup{}, err
	}

	return Backup{
		Things:        things,
		Channels:      channels,
		Connections:   connections,
		Groups:        groups,
		GroupPolicies: groupsPolicies,
	}, nil
}

func (ts *thingsService) Restore(ctx context.Context, token string, backup Backup) error {
	if err := ts.authorize(ctx, auth.RootSubject, token); err != nil {
		return err
	}

	for _, group := range backup.Groups {
		if _, err := ts.groups.Save(ctx, group); err != nil {
			return err
		}
	}

	if _, err := ts.things.Save(ctx, backup.Things...); err != nil {
		return err
	}

	if _, err := ts.channels.Save(ctx, backup.Channels...); err != nil {
		return err
	}

	for _, conn := range backup.Connections {
		if err := ts.channels.Connect(ctx, conn.ChannelID, []string{conn.ThingID}); err != nil {
			return err
		}
	}

	for _, g := range backup.GroupPolicies {
		gp := GroupPolicyByID{
			MemberID: g.MemberID,
			Policy:   g.Policy,
		}

		if err := ts.policies.SaveGroupPolicies(ctx, g.GroupID, gp); err != nil {
			return err
		}
	}

	return nil
}

func getTimestmap() time.Time {
	return time.Now().UTC().Round(time.Millisecond)
}

func (ts *thingsService) ListGroupThings(ctx context.Context, token string, groupID string, pm PageMetadata) (ThingsPage, error) {
	if err := ts.canAccessGroup(ctx, token, groupID, Read); err != nil {
		return ThingsPage{}, err
	}

	return ts.groups.RetrieveGroupThings(ctx, groupID, pm)
}

func (ts *thingsService) ListGroupChannels(ctx context.Context, token, groupID string, pm PageMetadata) (ChannelsPage, error) {
	if err := ts.canAccessGroup(ctx, token, groupID, Read); err != nil {
		return ChannelsPage{}, err
	}

	return ts.groups.RetrieveGroupChannels(ctx, groupID, pm)
}

func (ts *thingsService) authorize(ctx context.Context, subject, token string) error {
	req := &mainflux.AuthorizeReq{
		Token:   token,
		Subject: subject,
	}

	if _, err := ts.auth.Authorize(ctx, req); err != nil {
		return errors.Wrap(errors.ErrAuthorization, err)
	}

	return nil
}
