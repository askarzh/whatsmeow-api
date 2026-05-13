package mcp

import (
	"context"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type waLoginQROutput struct {
	Code     string `json:"code" jsonschema:"QR code string; render this as a QR image for the user to scan."`
	Terminal bool   `json:"terminal" jsonschema:"True when this is the final QR event (success or failure)."`
	Outcome  string `json:"outcome,omitempty" jsonschema:"Result text when terminal=true (e.g. paired, timeout, error)."`
}

type waLoginPhoneInput struct {
	PhoneNumber string `json:"phone_number" jsonschema:"E.164 without the leading '+', e.g. 14155551212."`
}

type waLoginPhoneOutput struct {
	Code     string `json:"code" jsonschema:"8-character pairing code the user types on their phone."`
	Terminal bool   `json:"terminal"`
	Outcome  string `json:"outcome,omitempty"`
}

func registerLoginTools(srv *mcpsdk.Server, d Deps) {
	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "wa_login_qr",
		Description: "Start QR pairing. Returns the first QR string emitted by the pairing channel. Render it as a QR code so the user can scan it from the WhatsApp mobile app.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, _ struct{}) (*mcpsdk.CallToolResult, waLoginQROutput, error) {
		ch, err := d.Service.LoginQR(ctx)
		if res, terr := mapErr(err, d.Logger); terr != nil || res != nil {
			return res, waLoginQROutput{}, terr
		}
		select {
		case ev := <-ch:
			return nil, waLoginQROutput{Code: ev.Code, Terminal: ev.Terminal, Outcome: ev.Outcome}, nil
		case <-ctx.Done():
			return toolErr("login_qr: timed out waiting for QR event"), waLoginQROutput{}, nil
		}
	})

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "wa_login_phone",
		Description: "Start pairing-code login. Returns the 8-character code the user types on their phone.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in waLoginPhoneInput) (*mcpsdk.CallToolResult, waLoginPhoneOutput, error) {
		ch, err := d.Service.LoginPhone(ctx, in.PhoneNumber)
		if res, terr := mapErr(err, d.Logger); terr != nil || res != nil {
			return res, waLoginPhoneOutput{}, terr
		}
		select {
		case ev := <-ch:
			return nil, waLoginPhoneOutput{Code: ev.Code, Terminal: ev.Terminal, Outcome: ev.Outcome}, nil
		case <-ctx.Done():
			return toolErr("login_phone: timed out waiting for pairing code"), waLoginPhoneOutput{}, nil
		}
	})

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "wa_logout",
		Description: "Sign the daemon out of the WhatsApp account. The user must re-pair via wa_login_qr or wa_login_phone after this.",
		Annotations: &mcpsdk.ToolAnnotations{DestructiveHint: boolPtr(true)},
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, _ struct{}) (*mcpsdk.CallToolResult, waOK, error) {
		if err := d.Service.Logout(ctx); err != nil {
			if res, terr := mapErr(err, d.Logger); terr != nil || res != nil {
				return res, waOK{}, terr
			}
		}
		return nil, waOK{OK: true}, nil
	})
}
