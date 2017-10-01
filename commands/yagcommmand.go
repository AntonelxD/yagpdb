package commands

import (
	"context"
	"fmt"
	log "github.com/Sirupsen/logrus"
	"github.com/jonas747/dcmd"
	"github.com/jonas747/discordgo"
	"github.com/jonas747/dutil/dstate"
	"github.com/jonas747/yagpdb/bot"
	"github.com/jonas747/yagpdb/common"
	"github.com/mediocregopher/radix.v2/redis"
	"github.com/pkg/errors"
	"strings"
	"time"
)

type ContextKey int

const (
	CtxKeyRedisClient ContextKey = iota
)

var (
	CategoryGeneral = &dcmd.Category{
		Name:        "General",
		Description: "General commands",
		HelpEmoji:   "ℹ️",
		EmbedColor:  0xe53939,
	}
	CategoryTool = &dcmd.Category{
		Name:        "Tools",
		Description: "Various miscellaneous commands",
		HelpEmoji:   "🔨",
		EmbedColor:  0xeaed40,
	}
	CategoryModeration = &dcmd.Category{
		Name:        "Moderation",
		Description: "Moderation commands",
		HelpEmoji:   "👮",
		EmbedColor:  0xdb0606,
	}
	CategoryFun = &dcmd.Category{
		Name:        "Fun",
		Description: "Various commands meant for entertainment",
		HelpEmoji:   "🎉",
		EmbedColor:  0x5ae26c,
	}
	CategoryDebug = &dcmd.Category{
		Name:        "Debug",
		Description: "Debug and other commands to inspect the bot",
		HelpEmoji:   "🖥",
		EmbedColor:  0,
	}
)

var (
	RKeyCommandCooldown = func(uID, cmd string) string { return "cmd_cd:" + uID + ":" + cmd }
	RKeyCommandLock     = func(uID, cmd string) string { return "cmd_lock:" + uID + ":" + cmd }

	CommandExecTimeout = time.Minute
)

// Slight extension to the simplecommand, it will check if the command is enabled in the HandleCommand func
// And invoke a custom handlerfunc with provided redis client
type YAGCommand struct {
	Name            string   // Name of command, what its called from
	Aliases         []string // Aliases which it can also be called from
	Description     string   // Description shown in non targetted help
	LongDescription string   // Longer description when this command was targetted

	Arguments      []*dcmd.ArgDef // Slice of argument definitions, ctx.Args will always be the same size as this slice (although the data may be nil)
	RequiredArgs   int            // Number of reuquired arguments, ignored if combos is specified
	ArgumentCombos [][]int        // Slice of argument pairs, will override RequiredArgs if specified
	ArgSwitches    []*dcmd.ArgDef // Switches for the commadn to use

	AllowEveryoneMention bool

	HideFromCommandsPage bool   // Set to  hide this command from the commands page
	Key                  string // GuildId is appended to the key, e.g if key is "test:", it will check for "test:<guildid>"
	CustomEnabled        bool   // Set to true to handle the enable check itself
	Default              bool   // The default state of this command
	Cooldown             int    // Cooldown in seconds before user can use it again
	CommandCategory      *dcmd.Category

	// Run is ran the the command has sucessfully been parsed
	// It returns a reply and an error
	// the reply can have a type of string, *MessageEmbed or error
	RunFunc dcmd.RunFunc
}

// CmdWithCategory puts the command in a category, mostly used for the help generation
func (yc *YAGCommand) Category() *dcmd.Category {
	return yc.CommandCategory
}

func (yc *YAGCommand) Descriptions(data *dcmd.Data) (short, long string) {
	return yc.Description, yc.LongDescription
}

func (yc *YAGCommand) ArgDefs(data *dcmd.Data) (args []*dcmd.ArgDef, required int, combos [][]int) {
	return yc.Arguments, yc.RequiredArgs, yc.ArgumentCombos
}

func (yc *YAGCommand) Switches() []*dcmd.ArgDef {
	return yc.ArgSwitches
}

func (yc *YAGCommand) Run(data *dcmd.Data) (interface{}, error) {

	// Track how long execution of a command took
	started := time.Now()
	defer func() {
		yc.logExecutionTime(time.Since(started), raw, trigger.Message.Author.Username)
	}()

	// Need a redis client to check cooldowns and retrieve command settings
	client, err := common.RedisPool.Get()
	if err != nil {
		log.WithError(err).Error("Failed retrieving redis client")
		return nil, errors.New("Failed retrieving redis client")
	}
	defer common.RedisPool.Put(client)

	err = common.BlockingLockRedisKey(client, RKeyCommandLock(trigger.Message.Author.ID, yc.Name), CommandExecTimeout*2, int((CommandExecTimeout + time.Second).Seconds()))
	if err != nil {
		return nil, errors.WithMessage(err, "Failed locking command")
	}
	defer common.UnlockRedisKey(client, RKeyCommandLock(trigger.Message.Author.ID, yc.Name))

	cState := bot.State.Channel(true, trigger.Message.ChannelID)
	if cState == nil {
		return nil, errors.New("Channel not found")
	}

	// Set up log entry for later use
	logEntry := &common.LoggedExecutedCommand{
		UserID:    trigger.Message.Author.ID,
		ChannelID: cState.ID(),

		Command:    yc.Name,
		RawCommand: raw,
		TimeStamp:  time.Now(),
	}

	if cState.Guild != nil {
		logEntry.GuildID = cState.Guild.ID()
	}

	resp, autoDel := yc.checkCanExecuteCommand(trigger, client, cState)
	if resp != "" {
		m, err := common.BotSession.ChannelMessageSend(cState.ID(), resp)
		if m != nil {
			return []*discordgo.Message{m}, err
		}
		return nil, err
	}

	log.WithField("channel", cState.ID()).WithField("author", trigger.Message.Author.ID).Info("Handling command: " + raw)

	runCtx, cancelExec := context.WithTimeout(ctx, CommandExecTimeout)
	defer cancelExec()

	// Run the command
	replies, err := yc.Command.HandleCommand(raw, trigger, context.WithValue(runCtx, CtxKeyRedisClient, client))

	if err != nil {
		if errors.Cause(err) == context.Canceled || errors.Cause(err) == context.DeadlineExceeded {
			common.BotSession.ChannelMessageSend(cState.Channel.ID, "Took longer than "+CommandExecTimeout.String()+" to handle command: `"+common.EscapeSpecialMentions(raw)+"`, Cancelled the command.")
		} else {
			logEntry.Error = err.Error()
			log.WithError(err).WithField("channel", cState.ID()).Error(yc.Name, ": failed handling command")
		}
	}

	logEntry.ResponseTime = int64(time.Since(started))

	if len(replies) > 0 && autoDel {
		go yc.deleteResponse(append(replies, trigger.Message))
	} else if autoDel {
		go yc.deleteResponse([]*discordgo.Message{trigger.Message})
	}

	// Log errors
	if err == nil {
		err = yc.SetCooldown(client, trigger.Message.Author.ID)
		if err != nil {
			log.WithError(err).Error("Failed setting cooldown")
		}
	}

	// Create command log entry
	err = common.GORM.Create(logEntry).Error
	if err != nil {
		log.WithError(err).Error("Failed creating command execution log")
	}

	return replies, err
}

// checkCanExecuteCommand returns a non empty string if this user cannot execute this command
func (cs *YAGCommand) checkCanExecuteCommand(trigger *commandsystem.TriggerData, client *redis.Client, cState *dstate.ChannelState) (resp string, autoDel bool) {
	// Check guild specific settings if not triggered from a DM
	var guild *dstate.GuildState

	if trigger.Source != commandsystem.SourceDM {

		guild = cState.Guild
		if guild == nil {
			return "You're not on a server?", false
		}

		var enabled bool
		var err error
		var role string
		// Check wether it's enabled or not
		enabled, role, autoDel, err = cs.Enabled(client, cState.ID(), guild)
		if err != nil {
			return "Bot is having isssues, contact the bot owner.", false
		}

		if !enabled {
			return fmt.Sprintf("The %q command is currently disabled on this server or channel. *(Control panel to enable/disable <https://%s>)*", cs.Name, common.Conf.Host), false
		}

		if role != "" {
			member, err := bot.GetMember(guild.ID(), trigger.Message.Author.ID)
			if err != nil {
				log.WithError(err).WithField("user", trigger.Message.Author.ID).WithField("guild", guild.ID()).Error("Failed fetchign guild member")
				return "Bot is having issues retrieving your member state", false
			}

			found := false
			for _, v := range member.Roles {
				if v == role {
					found = true
				}
			}

			if !found {
				guild.RLock()
				required := guild.Role(false, role)
				name := "Unknown?? (deleted maybe?)"
				if required != nil {
					name = required.Name
				}
				guild.RUnlock()
				return common.EscapeSpecialMentions(fmt.Sprintf("The **%s** role is required to use this command.", name)), false
			}
		}
	}

	// Check the command cooldown
	cdLeft, err := cs.CooldownLeft(client, trigger.Message.Author.ID)
	if err != nil {
		// Just pretend the cooldown is off...
		log.WithError(err).WithField("author", trigger.Message.Author.ID).Error("Failed checking command cooldown")
	}

	if cdLeft > 0 {
		return fmt.Sprintf("**%q:** You need to wait %d seconds before you can use the %q command again", common.EscapeSpecialMentions(trigger.Message.Author.Username), cdLeft, cs.Name), false
	}

	return
}

func (cs *YAGCommand) logExecutionTime(dur time.Duration, raw string, sender string) {
	log.Infof("Handled Command [%4dms] %s: %s", int(dur.Seconds()*1000), sender, raw)
}

func (cs *YAGCommand) deleteResponse(msgs []*discordgo.Message) {
	ids := make([]string, len(msgs))
	for k, msg := range msgs {
		ids[k] = msg.ID
	}

	if len(ids) < 1 {
		return // ...
	}

	time.Sleep(time.Second * 10)

	// Either do a bulk delete or single delete depending on how big the response was
	if len(ids) > 1 {
		common.BotSession.ChannelMessagesBulkDelete(msgs[0].ChannelID, ids)
	} else {
		common.BotSession.ChannelMessageDelete(msgs[0].ChannelID, msgs[0].ID)
	}
}

// customEnabled returns wether the command is enabled by it's custom key or not
func (cs *YAGCommand) customEnabled(client *redis.Client, guildID string) (bool, error) {
	// No special key so it's automatically enabled
	if cs.Key == "" || cs.CustomEnabled {
		return true, nil
	}

	// Check redis for settings
	reply := client.Cmd("GET", cs.Key+guildID)
	if reply.Err != nil {
		return false, reply.Err
	}

	enabled, _ := common.RedisBool(reply)

	if cs.Default {
		enabled = !enabled
	}

	if !enabled {
		return false, nil
	}

	return enabled, nil
}

// Enabled returns wether the command is enabled or not
func (cs *YAGCommand) Enabled(client *redis.Client, channel string, gState *dstate.GuildState) (enabled bool, requiredRole string, autodel bool, err error) {
	gState.RLock()
	defer gState.RUnlock()

	if cs.HideFromCommandsPage {
		return true, "", false, nil
	}

	ce, err := cs.customEnabled(client, gState.ID())
	if err != nil {
		return
	}
	if !ce {
		return false, "", false, nil
	}

	channels := make([]*discordgo.Channel, len(gState.Channels))
	i := 0
	for _, v := range gState.Channels {
		channels[i] = v.Channel
		i++
	}

	config := GetConfig(client, gState.ID(), channels)

	// Check overrides first to see if one was enabled, and if so determine if the command is available
	for _, override := range config.ChannelOverrides {
		if override.Channel == channel {
			if override.OverrideEnabled {
				// Find settings for this command
				for _, cmd := range override.Settings {
					if cmd.Cmd == cs.Name {
						return cmd.CommandEnabled, cmd.RequiredRole, cmd.AutoDelete, nil
					}
				}

			}
			break
		}
	}

	// Return from global settings then
	for _, cmd := range config.Global {
		if cmd.Cmd == cs.Name {
			if cs.Key != "" || cs.CustomEnabled {
				return true, cmd.RequiredRole, cmd.AutoDelete, nil
			}

			return cmd.CommandEnabled, cmd.RequiredRole, cmd.AutoDelete, nil
		}
	}

	log.WithField("command", cs.Name).WithField("guild", gState.ID()).Error("Command not in global commands")

	return false, "", false, nil
}

// CooldownLeft returns the number of seconds before a command can be used again
func (cs *YAGCommand) CooldownLeft(client *redis.Client, userID string) (int, error) {
	if cs.Cooldown < 1 || common.Testing {
		return 0, nil
	}

	ttl, err := client.Cmd("TTL", RKeyCommandCooldown(userID, cs.Name)).Int64()
	if ttl < 1 {
		return 0, nil
	}

	return int(ttl), err
}

// SetCooldown sets the cooldown of the command as it's defined in the struct
func (cs *YAGCommand) SetCooldown(client *redis.Client, userID string) error {
	if cs.Cooldown < 1 {
		return nil
	}
	now := time.Now().Unix()
	err := client.Cmd("SET", RKeyCommandCooldown(userID, cs.Name), now, "EX", cs.Cooldown).Err
	return err
}

// Keys and other sensitive information shouldnt be sent in error messages, but just in case it is
func CensorError(err error) string {
	toCensor := []string{
		common.BotSession.Token,
		common.Conf.ClientSecret,
	}

	out := err.Error()
	for _, c := range toCensor {
		out = strings.Replace(out, c, "", -1)
	}

	return out
}
