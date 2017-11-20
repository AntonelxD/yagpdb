package bot

import (
	"github.com/jonas747/dbstate"
)

// GetMessages Gets messages from state if possible, if not then it retrieves from the discord api
// Puts the messages in the state aswell
func GetMessages(channelID string, limit int, deleted bool) ([]*dbstate.MessageWithMeta, error) {

	msgs, err := State.LastChannelMessages(channelID, limit, deleted)
	if err != nil {
		return nil, err
	}

	newMsgs := make([]*dbstate.MessageWithMeta, len(msgs))

	// Reverse
	for i := 0; i < len(msgs); i++ {
		newMsgs[i] = msgs[len(msgs)-(i+1)]
	}

	return msgs, err

	// if limit < 1 {
	// 	return []*WrappedMessage{}, nil
	// }

	// // check state
	// msgBuf := make([]*WrappedMessage, limit)

	// cs := State.Channel(true, channelID)

	// cs.Owner.RLock()

	// n := len(msgBuf) - 1
	// for i := len(cs.Messages) - 1; i >= 0; i-- {
	// 	if !deleted {
	// 		if cs.Messages[i].Deleted {
	// 			continue
	// 		}
	// 	}
	// 	m := cs.Messages[i].Copy(true)
	// 	msgBuf[n] = &WrappedMessage{Message: m}
	// 	if cs.Messages[i].Deleted {
	// 		msgBuf[n].Deleted = true
	// 	}
	// 	n--
	// 	if n < 0 {
	// 		break
	// 	}
	// }

	// cs.Owner.RUnlock()

	// // Check if the state was full
	// if n >= limit {
	// 	return msgBuf, nil
	// }

	// // Not enough messages in state, retrieve them from the api
	// // Initialize the before id
	// before := ""
	// if n+1 < len(msgBuf) {
	// 	if msgBuf[n+1] != nil {
	// 		before = msgBuf[n+1].ID
	// 	}
	// }

	// // Start fetching from the api
	// for n >= 0 {
	// 	toFetch := n + 1
	// 	if toFetch > 100 {
	// 		toFetch = 100
	// 	}
	// 	msgs, err := common.BotSession.ChannelMessages(channelID, toFetch, before, "", "")
	// 	if err != nil {
	// 		return nil, err
	// 	}

	// 	log.WithField("num_msgs", len(msgs)).Info("API history req finished")

	// 	if len(msgs) < 1 { // Nothing more
	// 		break
	// 	}

	// 	// Copy over to buffer
	// 	for k, m := range msgs {
	// 		msgBuf[n-k] = &WrappedMessage{Message: m}
	// 	}

	// 	// Oldest message is last
	// 	before = msgs[len(msgs)-1].ID
	// 	n -= len(msgs)

	// 	if len(msgs) < toFetch {
	// 		break
	// 	}
	// }

	// // remove nil entries if it wasn't big enough
	// if n+1 > 0 {
	// 	msgBuf = msgBuf[n+1:]
	// }

	// // merge the current state with this new one and sort
	// cs.Owner.Lock()
	// defer cs.Owner.Unlock()

	// for _, m := range msgBuf {
	// 	cs.MessageAddUpdate(false, m.Message, -1, 0)
	// }

	// sort.Sort(DiscordMessages(cs.Messages))

	// cs.UpdateMessages(false, State.MaxChannelMessages, State.MaxMessageAge)

	// // Return at most limit results
	// if limit < len(msgBuf) {
	// 	return msgBuf[len(msgBuf)-limit:], nil
	// } else {
	// 	return msgBuf, nil
	// }
}
