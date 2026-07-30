package msgstore

import (
	"context"
	"fmt"
	"time"

	"codeberg.org/emersion/soju/database"
	"git.sr.ht/~sircmpwn/go-bare"
	"gopkg.in/irc.v4"
)

type dbMsgID struct {
	ID bare.Uint
}

func (dbMsgID) msgIDType() msgIDType {
	return msgIDDB
}

func parseDBMsgIDForOptions(s string, options *LoadMessageOptions) (int64, error) {
	var id dbMsgID
	networkID, entity, err := ParseMsgID(s, &id)
	if err != nil {
		return 0, err
	}
	if networkID != options.Network.ID || entity != options.Entity {
		return 0, fmt.Errorf("cannot find message ID: message ID doesn't match network/entity")
	}
	return int64(id.ID), nil
}

func formatDBMsgID(netID int64, target string, msgID int64) string {
	id := dbMsgID{bare.Uint(msgID)}
	return formatMsgID(netID, target, &id)
}

// dbMessageStore is a persistent store for IRC messages, that
// stores messages in the soju database.
type dbMessageStore struct {
	db database.Database
}

var (
	_ Store            = (*dbMessageStore)(nil)
	_ ChatHistoryStore = (*dbMessageStore)(nil)
	_ SearchStore      = (*dbMessageStore)(nil)
)

func NewDBStore(db database.Database) *dbMessageStore {
	return &dbMessageStore{
		db: db,
	}
}

func (ms *dbMessageStore) Close() error {
	return nil
}

func (ms *dbMessageStore) LastMsgID(ctx context.Context, network *database.Network, entity string, t time.Time) (string, error) {
	// TODO: what should we do with t?

	id, err := ms.db.GetMessageLastID(ctx, network.ID, entity)
	if err != nil {
		return "", err
	}
	return formatDBMsgID(network.ID, entity, id), nil
}

func (ms *dbMessageStore) LoadLatestID(ctx context.Context, id string, options *LoadMessageOptions) ([]*irc.Message, error) {
	msgID, err := parseDBMsgIDForOptions(id, options)
	if err != nil {
		return nil, err
	}

	l, err := ms.db.ListMessages(ctx, options.Network.ID, options.Entity, &database.MessageOptions{
		AfterID:   msgID,
		Limit:     options.Limit,
		Events:    options.Events,
		Reactions: options.Reactions,
		TakeLast:  true,
	})
	if err != nil {
		return nil, err
	}
	return filterHistoryMessages(l, options.Events, options.Reactions), nil
}

func (ms *dbMessageStore) Append(ctx context.Context, network *database.Network, entity string, msg *irc.Message) (string, error) {
	ids, err := ms.db.StoreMessages(ctx, network.ID, entity, []*irc.Message{msg})
	if err != nil {
		return "", err
	}
	return formatDBMsgID(network.ID, entity, ids[0]), nil
}

func (ms *dbMessageStore) ResolveMsgID(ctx context.Context, network *database.Network, entity, msgID string) (string, *irc.Message, error) {
	id, msg, err := ms.db.GetMessageIDByMsgID(ctx, network.ID, entity, msgID)
	if err != nil {
		return "", nil, err
	}
	if id == 0 {
		return "", nil, fmt.Errorf("cannot find message ID")
	}
	return formatDBMsgID(network.ID, entity, id), msg, nil
}

func (ms *dbMessageStore) loadIDRange(ctx context.Context, afterID, beforeID int64, takeLast bool, options *LoadMessageOptions) ([]*irc.Message, error) {
	l, err := ms.db.ListMessages(ctx, options.Network.ID, options.Entity, &database.MessageOptions{
		AfterID:   afterID,
		BeforeID:  beforeID,
		Limit:     options.Limit,
		Events:    options.Events,
		Reactions: options.Reactions,
		TakeLast:  takeLast,
	})
	if err != nil {
		return nil, err
	}
	return filterHistoryMessages(l, options.Events, options.Reactions), nil
}

func (ms *dbMessageStore) LoadBeforeID(ctx context.Context, id string, options *LoadMessageOptions) ([]*irc.Message, error) {
	beforeID, err := parseDBMsgIDForOptions(id, options)
	if err != nil {
		return nil, err
	}
	return ms.loadIDRange(ctx, 0, beforeID, true, options)
}

func (ms *dbMessageStore) LoadAfterID(ctx context.Context, id string, options *LoadMessageOptions) ([]*irc.Message, error) {
	afterID, err := parseDBMsgIDForOptions(id, options)
	if err != nil {
		return nil, err
	}
	return ms.loadIDRange(ctx, afterID, 0, false, options)
}

func (ms *dbMessageStore) LoadBetweenID(ctx context.Context, first, second string, options *LoadMessageOptions) ([]*irc.Message, error) {
	firstID, err := parseDBMsgIDForOptions(first, options)
	if err != nil {
		return nil, err
	}
	secondID, err := parseDBMsgIDForOptions(second, options)
	if err != nil {
		return nil, err
	}
	if firstID < secondID {
		return ms.loadIDRange(ctx, firstID, secondID, false, options)
	}
	return ms.loadIDRange(ctx, secondID, firstID, true, options)
}

func (ms *dbMessageStore) LoadBetween(ctx context.Context, first, second HistoryBound, options *LoadMessageOptions) ([]*irc.Message, error) {
	var firstID, secondID int64
	var err error
	if first.ID != "" {
		firstID, err = parseDBMsgIDForOptions(first.ID, options)
		if err != nil {
			return nil, err
		}
	}
	if second.ID != "" {
		secondID, err = parseDBMsgIDForOptions(second.ID, options)
		if err != nil {
			return nil, err
		}
	}

	forward := first.Timestamp.Before(second.Timestamp) ||
		(first.Timestamp.Equal(second.Timestamp) && firstID < secondID)
	lower, upper := first, second
	lowerID, upperID := firstID, secondID
	if !forward {
		lower, upper = second, first
		lowerID, upperID = secondID, firstID
	}

	l, err := ms.db.ListMessages(ctx, options.Network.ID, options.Entity, &database.MessageOptions{
		AfterTime:        lower.Timestamp,
		BeforeTime:       upper.Timestamp,
		AfterPositionID:  lowerID,
		BeforePositionID: upperID,
		Limit:            options.Limit,
		Events:           options.Events,
		Reactions:        options.Reactions,
		TakeLast:         !forward,
	})
	if err != nil {
		return nil, err
	}
	return filterHistoryMessages(l, options.Events, options.Reactions), nil
}

func (ms *dbMessageStore) ListTargets(ctx context.Context, network *database.Network, start, end time.Time, limit int, events bool) ([]ChatHistoryTarget, error) {
	var opts *database.MessageOptions
	if start.Before(end) {
		opts = &database.MessageOptions{
			AfterTime:  start,
			BeforeTime: end,
			Limit:      limit,
			Events:     events,
		}
	} else {
		opts = &database.MessageOptions{
			AfterTime:  end,
			BeforeTime: start,
			Limit:      limit,
			Events:     events,
			TakeLast:   true,
		}
	}
	l, err := ms.db.ListMessageLastPerTarget(ctx, network.ID, opts)
	if err != nil {
		return nil, err
	}
	targets := make([]ChatHistoryTarget, len(l))
	for i, v := range l {
		targets[i] = ChatHistoryTarget{
			Name:          v.Name,
			LatestMessage: v.LatestMessage,
		}
	}
	return targets, nil
}

func (ms *dbMessageStore) LoadBeforeTime(ctx context.Context, start, end time.Time, options *LoadMessageOptions) ([]*irc.Message, error) {
	l, err := ms.db.ListMessages(ctx, options.Network.ID, options.Entity, &database.MessageOptions{
		AfterTime:  end,
		BeforeTime: start,
		Limit:      options.Limit,
		Events:     options.Events,
		Reactions:  options.Reactions,
		TakeLast:   true,
	})
	if err != nil {
		return nil, err
	}
	return filterHistoryMessages(l, options.Events, options.Reactions), nil
}

func (ms *dbMessageStore) LoadAfterTime(ctx context.Context, start, end time.Time, options *LoadMessageOptions) ([]*irc.Message, error) {
	l, err := ms.db.ListMessages(ctx, options.Network.ID, options.Entity, &database.MessageOptions{
		AfterTime:  start,
		BeforeTime: end,
		Limit:      options.Limit,
		Events:     options.Events,
		Reactions:  options.Reactions,
	})
	if err != nil {
		return nil, err
	}
	return filterHistoryMessages(l, options.Events, options.Reactions), nil
}

func (ms *dbMessageStore) Search(ctx context.Context, network *database.Network, options *SearchMessageOptions) ([]*irc.Message, error) {
	l, err := ms.db.ListMessages(ctx, network.ID, options.In, &database.MessageOptions{
		AfterTime:  options.Start,
		BeforeTime: options.End,
		Limit:      options.Limit,
		Sender:     options.From,
		Text:       options.Text,
		TakeLast:   true,
	})
	if err != nil {
		return nil, err
	}
	return l, nil
}
