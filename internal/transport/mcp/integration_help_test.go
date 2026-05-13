package mcp_test

import (
	"context"
	"time"

	"github.com/askarzh/whatsmeow-api/internal/service"
	"github.com/askarzh/whatsmeow-api/internal/store"
	"github.com/askarzh/whatsmeow-api/internal/waclient"
)

type integMinService struct{}

var _ service.Service = integMinService{}

func (integMinService) Status(_ context.Context) (waclient.Status, error) {
	jid := "1@s.whatsapp.net"
	return waclient.Status{Connected: true, JID: &jid}, nil
}

func (integMinService) LoginQR(context.Context) (<-chan waclient.QREvent, error) {
	panic("integMinService.LoginQR not stubbed")
}
func (integMinService) LoginPhone(context.Context, string) (<-chan waclient.PairEvent, error) {
	panic("integMinService.LoginPhone not stubbed")
}
func (integMinService) Logout(context.Context) error { panic("integMinService.Logout not stubbed") }

func (integMinService) SendText(context.Context, string, string, string) (store.Message, error) {
	panic("integMinService.SendText not stubbed")
}

func (integMinService) ListChats(context.Context, time.Time, int, bool) ([]store.Chat, error) {
	panic("integMinService.ListChats not stubbed")
}
func (integMinService) GetChat(context.Context, string) (store.Chat, error) {
	panic("integMinService.GetChat not stubbed")
}
func (integMinService) ListMessages(context.Context, string, time.Time, int) ([]store.Message, error) {
	panic("integMinService.ListMessages not stubbed")
}
func (integMinService) SearchMessages(context.Context, string, int) ([]store.Message, error) {
	panic("integMinService.SearchMessages not stubbed")
}
func (integMinService) ListContacts(context.Context) ([]store.Contact, error) {
	panic("integMinService.ListContacts not stubbed")
}
func (integMinService) SearchContacts(context.Context, string, int) ([]store.Contact, error) {
	panic("integMinService.SearchContacts not stubbed")
}
func (integMinService) Stats(context.Context) (service.Stats, error) {
	panic("integMinService.Stats not stubbed")
}

func (integMinService) SendMedia(context.Context, service.SendMediaRequest) (store.Message, error) {
	panic("integMinService.SendMedia not stubbed")
}
func (integMinService) GetMediaRef(context.Context, string) (store.MediaRef, error) {
	panic("integMinService.GetMediaRef not stubbed")
}

func (integMinService) EditMessage(context.Context, string, string) (store.Message, error) {
	panic("integMinService.EditMessage not stubbed")
}
func (integMinService) DeleteMessage(context.Context, string) error {
	panic("integMinService.DeleteMessage not stubbed")
}

func (integMinService) SendReaction(context.Context, string, string) error {
	panic("integMinService.SendReaction not stubbed")
}
func (integMinService) ListReactions(context.Context, string) ([]store.Reaction, error) {
	panic("integMinService.ListReactions not stubbed")
}

func (integMinService) MarkMessageRead(context.Context, string) error {
	panic("integMinService.MarkMessageRead not stubbed")
}
func (integMinService) SendTyping(context.Context, string, string) error {
	panic("integMinService.SendTyping not stubbed")
}
func (integMinService) ListReceipts(context.Context, string) ([]store.Receipt, error) {
	panic("integMinService.ListReceipts not stubbed")
}

func (integMinService) CreateGroup(context.Context, string, []string) (waclient.Group, error) {
	panic("integMinService.CreateGroup not stubbed")
}
func (integMinService) ListGroupMembers(context.Context, string) ([]waclient.GroupMember, error) {
	panic("integMinService.ListGroupMembers not stubbed")
}
func (integMinService) UpdateGroupMembers(context.Context, string, string, []string) ([]waclient.ParticipantChange, error) {
	panic("integMinService.UpdateGroupMembers not stubbed")
}
func (integMinService) LeaveGroup(context.Context, string) error {
	panic("integMinService.LeaveGroup not stubbed")
}
