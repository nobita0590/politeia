package main

import (
	"fmt"

	"github.com/google/uuid"

	"github.com/decred/politeia/politeiawww/api/v1"
	"github.com/decred/politeia/politeiawww/database"
)

type EventT int

type EventManager struct {
	Listeners map[EventT][]chan interface{}
}

const (
	EventTypeInvalid                EventT = 0
	EventTypeProposalSubmitted      EventT = 1
	EventTypeProposalStatusChange   EventT = 2
	EventTypeProposalEdited         EventT = 3
	EventTypeProposalVoteStarted    EventT = 4
	EventTypeProposalVoteAuthorized EventT = 5
	EventTypeProposalVoteFinished   EventT = 6
	EventTypeUserManage             EventT = 7
)

type EventDataProposalSubmitted struct {
	CensorshipRecord *v1.CensorshipRecord
	ProposalName     string
	User             *database.User
}

type EventDataProposalStatusChange struct {
	Proposal          *v1.ProposalRecord
	SetProposalStatus *v1.SetProposalStatus
	AdminUser         *database.User
}

type EventDataProposalEdited struct {
	Proposal *v1.ProposalRecord
}

type EventDataProposalVoteStarted struct {
	AdminUser *database.User
	StartVote *v1.StartVote
}

type EventDataProposalVoteAuthorized struct {
	AuthorizeVote *v1.AuthorizeVote
	User          *database.User
}

type EventDataUserManage struct {
	AdminUser  *database.User
	User       *database.User
	ManageUser *v1.ManageUser
}

func (b *backend) getProposalAuthor(proposal *v1.ProposalRecord) (*database.User, error) {
	if proposal.UserId == "" {
		proposal.UserId = b.userPubkeys[proposal.PublicKey]
	}

	userID, err := uuid.Parse(proposal.UserId)
	if err != nil {
		return nil, fmt.Errorf("parse UUID: %v", err)
	}
	return b.db.UserGetById(userID)
}

func (b *backend) initEventManager() {
	b.Lock()
	defer b.Unlock()

	b.eventManager = &EventManager{}

	b._setupProposalSubmittedEmailNotification()

	b._setupProposalStatusChangeEmailNotification()
	b._setupProposalStatusChangeLogging()

	b._setupProposalEditedEmailNotification()

	b._setupProposalVoteStartedEmailNotification()
	b._setupProposalVoteStartedLogging()

	b._setupProposalVoteAuthorizedEmailNotification()

	b._setupUserManageLogging()
}

func (b *backend) _setupProposalSubmittedEmailNotification() {
	if b.cfg.SMTP == nil {
		return
	}

	ch := make(chan interface{})
	go func() {
		for data := range ch {
			ps, ok := data.(EventDataProposalSubmitted)
			if !ok {
				log.Errorf("invalid event data")
				continue
			}

			err := b.emailAdminsForNewSubmittedProposal(
				ps.CensorshipRecord.Token, ps.ProposalName,
				ps.User.Username, ps.User.Email)
			if err != nil {
				log.Errorf("email all admins for new submitted proposal %v: %v",
					ps.CensorshipRecord.Token, err)
			}
		}
	}()
	b.eventManager._register(EventTypeProposalSubmitted, ch)
}

func (b *backend) _setupProposalStatusChangeEmailNotification() {
	if b.cfg.SMTP == nil {
		return
	}

	ch := make(chan interface{})
	go func() {
		for data := range ch {
			psc, ok := data.(EventDataProposalStatusChange)
			if !ok {
				log.Errorf("invalid event data")
				continue
			}

			if psc.SetProposalStatus.ProposalStatus != v1.PropStatusPublic &&
				psc.SetProposalStatus.ProposalStatus != v1.PropStatusCensored {
				continue
			}

			b.RLock()
			author, err := b.getProposalAuthor(psc.Proposal)
			if err != nil {
				b.RUnlock()
				log.Errorf("cannot fetch author for proposal: %v", err)
				continue
			}
			b.RUnlock()

			switch psc.SetProposalStatus.ProposalStatus {
			case v1.PropStatusPublic:
				err = b.emailAuthorForVettedProposal(psc.Proposal, author,
					psc.AdminUser)
				if err != nil {
					log.Errorf("email author for vetted proposal %v: %v",
						psc.Proposal.CensorshipRecord.Token, err)
				}
				err = b.emailUsersForVettedProposal(psc.Proposal, author,
					psc.AdminUser)
				if err != nil {
					log.Errorf("email users for vetted proposal %v: %v",
						psc.Proposal.CensorshipRecord.Token, err)
				}
			case v1.PropStatusCensored:
				err = b.emailAuthorForCensoredProposal(psc.Proposal, author,
					psc.AdminUser)
				if err != nil {
					log.Errorf("email author for censored proposal %v: %v",
						psc.Proposal.CensorshipRecord.Token, err)
				}
			default:
			}
		}
	}()
	b.eventManager._register(EventTypeProposalStatusChange, ch)
}

func (b *backend) _setupProposalStatusChangeLogging() {
	ch := make(chan interface{})
	go func() {
		for data := range ch {
			psc, ok := data.(EventDataProposalStatusChange)
			if !ok {
				log.Errorf("invalid event data")
				continue
			}

			// Log the action in the admin log.
			err := b.logAdminProposalAction(psc.AdminUser,
				psc.Proposal.CensorshipRecord.Token,
				fmt.Sprintf("set proposal status to %v",
					v1.PropStatus[psc.SetProposalStatus.ProposalStatus]),
				psc.SetProposalStatus.StatusChangeMessage)

			if err != nil {
				log.Errorf("could not log action to file: %v", err)
			}
		}
	}()
	b.eventManager._register(EventTypeProposalStatusChange, ch)
}

func (b *backend) _setupProposalEditedEmailNotification() {
	if b.cfg.SMTP == nil {
		return
	}

	ch := make(chan interface{})
	go func() {
		for data := range ch {
			pe, ok := data.(EventDataProposalEdited)
			if !ok {
				log.Errorf("invalid event data")
				continue
			}

			if pe.Proposal.Status != v1.PropStatusPublic {
				continue
			}

			b.RLock()
			author, err := b.getProposalAuthor(pe.Proposal)
			if err != nil {
				b.RUnlock()
				log.Errorf("cannot fetch author for proposal: %v", err)
				continue
			}
			b.RUnlock()

			err = b.emailUsersForEditedProposal(pe.Proposal, author)
			if err != nil {
				log.Errorf("email users for edited proposal %v: %v",
					pe.Proposal.CensorshipRecord.Token, err)
			}
		}
	}()
	b.eventManager._register(EventTypeProposalEdited, ch)
}

func (b *backend) _setupProposalVoteStartedEmailNotification() {
	if b.cfg.SMTP == nil {
		return
	}

	ch := make(chan interface{})
	go func() {
		for data := range ch {
			pvs, ok := data.(EventDataProposalVoteStarted)
			if !ok {
				log.Errorf("invalid event data")
				continue
			}

			b.RLock()
			token := pvs.StartVote.Vote.Token
			p, err := b._getInventoryRecord(token)
			if err != nil {
				b.RUnlock()
				log.Errorf("proposal not found: %v", err)
				continue
			}
			proposal := b._convertPropFromInventoryRecord(p)

			author, err := b.getProposalAuthor(&proposal)
			if err != nil {
				b.RUnlock()
				log.Errorf("cannot fetch author for proposal: %v", err)
				continue
			}
			b.RUnlock()

			err = b.emailUsersForProposalVoteStarted(&proposal, author,
				pvs.AdminUser)
			if err != nil {
				log.Errorf("email all admins for new submitted proposal %v: %v",
					token, err)
			}
		}
	}()
	b.eventManager._register(EventTypeProposalVoteStarted, ch)
}

func (b *backend) _setupProposalVoteStartedLogging() {
	ch := make(chan interface{})
	go func() {
		for data := range ch {
			pvs, ok := data.(EventDataProposalVoteStarted)
			if !ok {
				log.Errorf("invalid event data")
				continue
			}

			// Log the action in the admin log.
			err := b.logAdminProposalAction(pvs.AdminUser,
				pvs.StartVote.Vote.Token, "start vote", "")
			if err != nil {
				log.Errorf("could not log action to file: %v", err)
			}
		}
	}()
	b.eventManager._register(EventTypeProposalVoteStarted, ch)
}

func (b *backend) _setupProposalVoteAuthorizedEmailNotification() {
	if b.cfg.SMTP == nil {
		return
	}

	ch := make(chan interface{})
	go func() {
		for data := range ch {
			pvs, ok := data.(EventDataProposalVoteAuthorized)
			if !ok {
				log.Errorf("invalid event data")
				continue
			}

			token := pvs.AuthorizeVote.Token
			p, err := b.getInventoryRecord(token)
			if err != nil {
				log.Errorf("proposal not found: %v", err)
				continue
			}
			proposal := b._convertPropFromInventoryRecord(p)

			err = b.emailAdminsForProposalVoteAuthorized(&proposal, pvs.User)
			if err != nil {
				log.Errorf("email all admins for new submitted proposal %v: %v",
					token, err)
			}
		}
	}()
	b.eventManager._register(EventTypeProposalVoteAuthorized, ch)
}

func (b *backend) _setupUserManageLogging() {
	ch := make(chan interface{})
	go func() {
		for data := range ch {
			ue, ok := data.(EventDataUserManage)
			if !ok {
				log.Errorf("invalid event data")
				continue
			}

			// Log the action in the admin log.
			err := b.logAdminUserAction(ue.AdminUser, ue.User,
				ue.ManageUser.Action, ue.ManageUser.Reason)
			if err != nil {
				log.Errorf("could not log action to file: %v", err)
			}
		}
	}()
	b.eventManager._register(EventTypeUserManage, ch)
}

// _register adds a listener channel for the given event type.
//
// This function must be called WITH the mutex held.
func (e *EventManager) _register(eventType EventT, listenerToAdd chan interface{}) {
	if e.Listeners == nil {
		e.Listeners = make(map[EventT][]chan interface{})
	}

	if _, ok := e.Listeners[eventType]; ok {
		e.Listeners[eventType] = append(e.Listeners[eventType], listenerToAdd)
	} else {
		e.Listeners[eventType] = []chan interface{}{listenerToAdd}
	}
}

// _unregister removes the given listener channel for the given event type.
//
// This function must be called WITH the mutex held.
func (e *EventManager) _unregister(eventType EventT, listenerToRemove chan interface{}) {
	listeners, ok := e.Listeners[eventType]
	if !ok {
		return
	}

	for i, listener := range listeners {
		if listener == listenerToRemove {
			e.Listeners[eventType] = append(e.Listeners[eventType][:i],
				e.Listeners[eventType][i+1:]...)
			break
		}
	}
}

// _fireEvent iterates all listener channels for the given event type and
// passes the given data to it.
//
// This function must be called WITH the mutex held.
func (e *EventManager) _fireEvent(eventType EventT, data interface{}) {
	listeners, ok := e.Listeners[eventType]
	if !ok {
		return
	}

	for _, listener := range listeners {
		go func(listener chan interface{}) {
			listener <- data
		}(listener)
	}
}