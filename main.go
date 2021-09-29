package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/bwmarrin/discordgo"
)

type HistoricalData struct {
	Member  *discordgo.Member
	balance int
}

type Participant struct {
	Member *discordgo.Member
	Roll   int64
}

type Table struct {
	Columns []Column
	Rows    []Row
	width   int
}

type Column struct {
	Name   string
	Length int
}

type Row struct {
	ColumnValues []string
}

type GamblingGame struct {
	initiator       *discordgo.Member
	messageID       string
	resultMessageID string
	ChannelID       string
	participants    []Participant
	amount          int64
	initialized     bool
	lastCall        bool
	closed          bool
	lowestRoll      Participant
	highestRoll     Participant
	winner          *discordgo.Member
}

// Variables used for command line parameters
var (
	Token   string
	GuildID string
)

var (
	s *discordgo.Session
)

var (
	activeGame GamblingGame
	table      Table
	tableTitle string
	History    HistoricalData
)

var (
	commands = []*discordgo.ApplicationCommand{
		{
			Name:        "start",
			Description: "Starts a new gambling session, defaults to 100k",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionInteger,
					Name:        "amount",
					Description: "Set a custom roll amount",
					Required:    false,
				},
			},
		},
	}
	commandHandlers = map[string]func(s *discordgo.Session, i *discordgo.InteractionCreate){
		"start": func(s *discordgo.Session, i *discordgo.InteractionCreate) {

			if activeGame.messageID != "" {
				s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
					Type: discordgo.InteractionResponseChannelMessageWithSource,
					Data: &discordgo.InteractionResponseData{
						Content: "There's already a game in progress bro",
					},
				})
			} else {
				var amount int64 = 100000
				initiator := i.Member.User.ID
				if len(i.ApplicationCommandData().Options) != 0 {
					amount = i.ApplicationCommandData().Options[0].IntValue()
				}

				message := fmt.Sprintf(`New %vk Rolling session started by <@%s>!
			:one: to participate :exclamation: for last call and :ballot_box_with_check: to start`, (amount / 1000), initiator)
				s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
					Type: discordgo.InteractionResponseChannelMessageWithSource,
					Data: &discordgo.InteractionResponseData{
						Content: message,
					},
				})

				// Add correct reactions to the Message
				chanID := i.ChannelID
				k, _ := s.Channel(chanID)
				err := s.MessageReactionAdd(chanID, k.LastMessageID, "1️⃣")
				e := s.MessageReactionAdd(chanID, k.LastMessageID, "❗")
				er := s.MessageReactionAdd(chanID, k.LastMessageID, "☑️")
				// s.MessageReactionAdd(chanID, k.LastMessageID, "🥲")

				if err != nil || e != nil || er != nil {
					fmt.Println("error", err, e, er)
				}

				activeGame = GamblingGame{
					initiator:   i.Member,
					messageID:   k.LastMessageID,
					ChannelID:   i.ChannelID,
					amount:      amount,
					initialized: true,
				}
			}
		},
	}
)

func init() {
	flag.StringVar(&Token, "t", "", "Bot Token")
	flag.StringVar(&GuildID, "g", "", "Guild ID")
	flag.Parse()
}

func main() {

	rand.Seed(time.Now().UnixNano())
	History = HistoricalData{}
	// Create a new Discord session using the provided bot token.
	s, err := discordgo.New("Bot " + Token)
	if err != nil {
		fmt.Println("error creating Discord session,", err)
		return
	}

	s.AddHandler(func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		if h, ok := commandHandlers[i.ApplicationCommandData().Name]; ok {
			h(s, i)
		}
	})

	s.AddHandler(handleReactions)

	// Open a websocket connection to Discord and begin listening.
	err = s.Open()
	if err != nil {
		fmt.Println("error opening connection,", err)
		return
	}

	for _, v := range commands {
		_, err := s.ApplicationCommandCreate(s.State.User.ID, GuildID, v)
		if err != nil {
			log.Panicf("Cannot create '%v' command: %v", v.Name, err)
		}
	}

	// Wait here until CTRL-C or other term signal is received.
	fmt.Println("Bot is now running.  Press CTRL-C to exit.")
	sc := make(chan os.Signal, 1)
	signal.Notify(sc, syscall.SIGINT, syscall.SIGTERM, os.Interrupt, os.Kill)
	<-sc

	// Cleanly close down the Discord session.
	s.Close()
}

func contains(participants []Participant, newParticipantID string) bool {
	for _, participant := range participants {
		if participant.Member.User.ID == newParticipantID {
			return true
		}
	}

	return false
}

func handleReactions(s *discordgo.Session, r *discordgo.MessageReactionAdd) {
	fmt.Println("Activegame", activeGame)
	fmt.Println("Participants", activeGame.participants)
	if activeGame.messageID != "" && r.MessageID == activeGame.messageID && activeGame.initialized {
		fmt.Println("Active message getting here")
		if r.UserID != s.State.User.ID {
			fmt.Println("Reaction was from someone else than bot")
			if r.Emoji.Name == "1️⃣" && !activeGame.closed {
				fmt.Print("Someone wants to participate")
				if !contains(activeGame.participants, r.UserID) {
					member, err := s.GuildMember(GuildID, r.UserID)
					if err != nil {
						fmt.Print("Error Fetching Member: ", err)
					}
					activeGame.participants = append(activeGame.participants, Participant{Member: member, Roll: 0})
				}
			} else if r.Emoji.Name == "❗" && r.UserID == activeGame.initiator.User.ID && !activeGame.lastCall && !activeGame.closed {
				fmt.Print("Last call invoked")
				activeGame.lastCall = true
				_, err := s.ChannelMessageSendReply(activeGame.ChannelID, "Last call for this game!", &discordgo.MessageReference{
					MessageID: activeGame.messageID,
					ChannelID: activeGame.ChannelID,
					GuildID:   GuildID})

				if err != nil {
					fmt.Println("Error sending last call message: ", err)
				}
			} else if r.Emoji.Name == "☑️" && r.UserID == activeGame.initiator.User.ID && activeGame.lastCall && !activeGame.closed {
				activeGame.closed = true
				if len(activeGame.participants) < 2 {
					_, err := s.ChannelMessageSendReply(activeGame.ChannelID, "Not enough participants to start a roll for this, create a new game to try again", &discordgo.MessageReference{
						MessageID: activeGame.messageID,
						ChannelID: activeGame.ChannelID,
						GuildID:   GuildID})

					if err != nil {
						fmt.Println("Error sending last call message: ", err)
					}

					activeGame = GamblingGame{}
				} else {
					rollGame(s)
				}
			} else if r.Emoji.Name == "🥲" {
				rollGame(s)
			}
		}
	}
}

func handleRoll(p *Participant) {
	roll := rand.Intn(int(activeGame.amount))
	p.Roll = int64(roll)
	if roll > int(activeGame.highestRoll.Roll) {
		activeGame.highestRoll = *p
	}
	if activeGame.lowestRoll.Roll == 0 || roll < int(activeGame.lowestRoll.Roll) {
		activeGame.lowestRoll = *p
	}
}

func endGame(s *discordgo.Session) {
	if activeGame.highestRoll.Member.User.ID != activeGame.lowestRoll.Member.User.ID {
		activeGame.winner = activeGame.highestRoll.Member
		payout := activeGame.highestRoll.Roll - activeGame.lowestRoll.Roll

		s.ChannelMessageSend(activeGame.ChannelID, fmt.Sprintf("<@%s> needs to pay <@%s> %dg for this game!", activeGame.lowestRoll.Member.User.ID, activeGame.winner.User.ID, payout))

	} else {
		s.ChannelMessageSend(activeGame.ChannelID, "There was either a tie or I fucked up somehow, try again suckers")
	}

	activeGame = GamblingGame{}
}

func rollGame(s *discordgo.Session) {
	m, err := s.ChannelMessageSendReply(activeGame.ChannelID, "Starting rolling for this game...", &discordgo.MessageReference{
		MessageID: activeGame.messageID,
		ChannelID: activeGame.ChannelID,
		GuildID:   GuildID})

	if err != nil {
		fmt.Println("Error sending roll message: ", err)
	}

	activeGame.resultMessageID = m.ID

	time.Sleep(3 * time.Second)

	tableTitle = "GENERATING TABLE \n"
	s.ChannelMessageEdit(activeGame.ChannelID, activeGame.resultMessageID, generateTable())

	time.Sleep(2 * time.Second)

	for i, part := range activeGame.participants {
		tableTitle = fmt.Sprintf("RESULTS FOR %s ADDED...... \n", part.Member.User.Username)
		handleRoll(&activeGame.participants[i])
		s.ChannelMessageEdit(activeGame.ChannelID, activeGame.resultMessageID, generateTable())
		time.Sleep(3 * time.Second)
	}

	endGame(s)
}

func generateTable() string {
	standings := activeGame.participants
	sort.Slice(standings, func(i, j int) bool {
		return standings[i].Roll > standings[j].Roll
	})

	fmt.Println("amount", activeGame.amount)
	fmt.Println("amount len", len(strconv.FormatInt((activeGame.amount), 10))+5)
	table = Table{Columns: []Column{{Name: "Name", Length: (GetLongestName() + 12)}, {Name: "Amount", Length: (len(strconv.FormatInt((activeGame.amount), 10)) + 12)}}}

	for _, c := range table.Columns {
		table.width = table.width + c.Length
	}

	for _, pm := range activeGame.participants {
		table.Rows = append(table.Rows, Row{ColumnValues: getColumnValuesForParticipant(pm.Member.User.ID)})
	}

	fmt.Println("table", table)
	var builder strings.Builder
	builder.WriteString("```")
	builder.WriteString(tableTitle)
	fmt.Fprintf(&builder, "%s \n", generateTopOrBottom("┏", "┓"))
	fmt.Fprintf(&builder, "%s \n", generateHeader())
	fmt.Fprintf(&builder, "%s \n", generateDivider())

	for _, r := range table.Rows {
		fmt.Fprintf(&builder, "%s \n", generateRow(r.ColumnValues))
	}
	fmt.Fprintf(&builder, "%s", generateTopOrBottom("┗", "┛"))
	builder.WriteString("```")
	return builder.String()
}

func getColumnValuesForParticipant(ParticipantID string) []string {
	p, err := findParticipant(activeGame.participants, ParticipantID)

	if err != nil {
		fmt.Println(err)
	}

	return []string{p.Member.User.Username, strconv.FormatInt((p.Roll), 10)}
}

func generateHeader() string {
	var b strings.Builder

	for _, c := range table.Columns {
		b.WriteString("┃")
		b.WriteString(center(c.Name, (c.Length - len(c.Name)), " "))
	}
	b.WriteString("┃")
	return b.String()
}

func center(s string, n int, fill string) string {
	div := n / 2

	return strings.Repeat(fill, div) + s + strings.Repeat(fill, div)
}

func generateDivider() string {
	var b strings.Builder

	for i, c := range table.Columns {
		if i == 0 {
			b.WriteString("┣")
		} else {
			b.WriteString("│")
		}
		for i := (c.Length - 1); i > 0; i-- {
			b.WriteString("━")
		}
	}
	b.WriteString("┫")
	return b.String()
}

func generateTopOrBottom(left string, right string) string {
	var b strings.Builder
	b.WriteString(left)
	for i := 0; i < table.width; i++ {
		b.WriteString("━")
	}

	b.WriteString(right)
	return b.String()
}

func generateRow(values []string) string {
	var b strings.Builder

	b.WriteString("┃")
	for i, val := range values {
		b.WriteString(center(val, (table.Columns[i].Length - len(val)), " "))
	}
	b.WriteString("┃")
	return b.String()
}

func GetLongestName() int {
	longestName := 0

	for _, m := range activeGame.participants {

		if len(m.Member.User.Username) > longestName {
			longestName = len(m.Member.User.Username)
		}
	}

	return longestName
}

func findParticipant(Participants []Participant, pID string) (Participant, error) {
	for _, p := range Participants {
		if p.Member.User.ID == pID {
			return p, nil
		}
	}

	return Participant{}, errors.New("No Participant found with that Id")
}
