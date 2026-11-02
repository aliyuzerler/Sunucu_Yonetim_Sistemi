//go:build unix

// Bağımsız Faz 11 testi bulgusu IT-01 (test boşluğu düzeltmesi): mail
// ailesi broker katmanında gate/şema/yürütme testleri OLMADAN birleştirilmişti
// (db/fs ailelerindeki gibi üç-katman kanıt). Bu dosya boşluğu kapatır:
//   - Kapı VARSAYILAN KAPALI: Config.Mail nil → tipli unsupported.
//   - Strict şema negatifleri: broker'a varan kötü payload runner'a gitmez.
//   - Yürütme: runner bağlıyken happy yolu + runner hata kategorisi.

package broker

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/mailops"
)

type fakeMailRunner struct {
	err error
}

func (f *fakeMailRunner) Preflight(context.Context) (mailops.PreflightResult, error) {
	return mailops.PreflightResult{Hostname: "n1"}, f.err
}
func (f *fakeMailRunner) Status(context.Context) (mailops.StatusResult, error) {
	return mailops.StatusResult{Domains: 1}, f.err
}
func (f *fakeMailRunner) DomainList(context.Context, int) ([]mailops.DomainInfo, error) {
	return []mailops.DomainInfo{{Name: "mail.example.com"}}, f.err
}
func (f *fakeMailRunner) DomainCreate(context.Context, string) (mailops.MutateResult, error) {
	return mailops.MutateResult{Applied: true, Reloaded: true}, f.err
}
func (f *fakeMailRunner) DomainDelete(context.Context, string) (mailops.MutateResult, error) {
	return mailops.MutateResult{Applied: true}, f.err
}
func (f *fakeMailRunner) MailboxList(context.Context, string, int) ([]mailops.MailboxInfo, error) {
	return []mailops.MailboxInfo{{Address: "a@mail.example.com", QuotaBytes: 1 << 30}}, f.err
}
func (f *fakeMailRunner) MailboxCreate(context.Context, string, string) (map[string]any, error) {
	return map[string]any{"applied": true}, f.err
}
func (f *fakeMailRunner) MailboxDelete(context.Context, string) (map[string]any, error) {
	return map[string]any{"applied": true}, f.err
}
func (f *fakeMailRunner) AliasList(context.Context, string, int) ([]mailops.AliasInfo, error) {
	return []mailops.AliasInfo{{Alias: "i@mail.example.com", Target: "a@mail.example.com"}}, f.err
}
func (f *fakeMailRunner) AliasCreate(context.Context, string, string) (mailops.MutateResult, error) {
	return mailops.MutateResult{Applied: true}, f.err
}
func (f *fakeMailRunner) AliasDelete(context.Context, string) (mailops.MutateResult, error) {
	return mailops.MutateResult{Applied: true}, f.err
}
func (f *fakeMailRunner) QuotaSet(context.Context, string, int64) (mailops.MutateResult, error) {
	return mailops.MutateResult{Applied: true}, f.err
}
func (f *fakeMailRunner) ConfigCheck(context.Context) (mailops.ConfigCheckResult, error) {
	return mailops.ConfigCheckResult{Postfix: mailops.CheckOutcome{State: "pass"}}, f.err
}
func (f *fakeMailRunner) QueueList(context.Context, int) ([]mailops.QueueEntry, error) {
	return []mailops.QueueEntry{{QueueID: "Q1"}}, f.err
}
func (f *fakeMailRunner) LimitsGet(context.Context) (mailops.LimitsGetResult, error) {
	return mailops.LimitsGetResult{Limits: map[string]string{"message_size_limit": "10240000"}}, f.err
}
func (f *fakeMailRunner) LimitsSet(context.Context, map[string]int64) (mailops.LimitsSetResult, error) {
	return mailops.LimitsSetResult{Applied: true}, f.err
}

var _ MailRunner = (*fakeMailRunner)(nil)

// Kapı VARSAYILAN KAPALI: Config.Mail nil → her mail op'u tipli unsupported.
func TestMailFamilyDisabledByDefault(t *testing.T) {
	srv := NewServer(Config{AllowedUIDs: []uint32{uint32(os.Getuid())}})
	gate := []struct {
		op  string
		req string
	}{
		{OpMailPreflight, `{}`},
		{OpMailStatus, `{}`},
		{OpMailDomainList, `{}`},
		{OpMailDomainCreate, `{"domain":"mail.example.com"}`},
		{OpMailDomainDelete, `{"domain":"mail.example.com"}`},
		{OpMailBoxList, `{}`},
		{OpMailBoxCreate, `{"domain":"mail.example.com","local":"ali"}`},
		{OpMailBoxDelete, `{"mailbox":"ali@mail.example.com"}`},
		{OpMailAliasList, `{}`},
		{OpMailAliasCreate, `{"alias":"i@mail.example.com","target":"a@mail.example.com"}`},
		{OpMailAliasDelete, `{"alias":"i@mail.example.com"}`},
		{OpMailQuotaSet, `{"mailbox":"ali@mail.example.com","bytes":1073741824}`},
		{OpMailConfigCheck, `{}`},
		{OpMailQueueList, `{}`},
		{OpMailLimitsGet, `{}`},
		{OpMailLimitsSet, `{"limits":{"message_size_limit":20971520}}`},
	}
	for _, g := range gate {
		req := &Request{V: ProtocolV1, Operation: g.op, RequestID: "t", Params: []byte(g.req)}
		call, verr := validateRequest(req)
		if verr != nil {
			t.Fatalf("%s şema geçmeli (kapı yürütme katmanında): %v", g.op, verr)
		}
		res := srv.execute(req, call)
		if res.Status != StatusRejected || res.ErrorCategory != CatUnsupportedOp {
			t.Fatalf("%s kapalıyken unsupported beklenir: %+v", g.op, res)
		}
	}
}

// Strict şema negatifleri (validateRequest katmanı).
func TestMailFamilyValidation(t *testing.T) {
	negatives := []struct {
		op  string
		req string
	}{
		{OpMailDomainCreate, `{"domain":"Mail.example.com"}`},
		{OpMailDomainCreate, `{"domain":"mail"}`},
		{OpMailDomainCreate, `{"domain":"mail.example.com","x":1}`},
		{OpMailDomainDelete, `{"domain":"-bad.example"}`},
		{OpMailBoxCreate, `{"domain":"mail.example.com","local":".ali"}`},
		{OpMailBoxCreate, `{"domain":"mail.example.com","local":"a..b"}`},
		{OpMailBoxDelete, `{"mailbox":"not-an-address"}`},
		{OpMailBoxDelete, `{"mailbox":"ali@Mail.example.com"}`},
		{OpMailAliasCreate, `{"alias":"a@x.com","target":"a@x.com"}`},
		{OpMailAliasCreate, `{"alias":"a@x.com"}`},
		{OpMailQuotaSet, `{"mailbox":"a@x.com","bytes":1024}`},
		{OpMailQuotaSet, `{"mailbox":"a@x.com","bytes":2199023255552}`},
		{OpMailQueueList, `{"limit":201}`},
		{OpMailLimitsSet, `{"limits":{}}`},
		{OpMailLimitsSet, `{"limits":{"evil_key":1}}`},
		{OpMailLimitsSet, `{"limits":{"message_size_limit":999999999999}}`},
		{OpMailLimitsSet, `{"limits":{"message_size_limit":20971520},"extra":1}`},
		{OpMailPreflight, `{"x":1}`},
	}
	for i, n := range negatives {
		req := &Request{V: ProtocolV1, Operation: n.op, RequestID: "neg", Params: []byte(n.req)}
		if _, err := validateRequest(req); err == nil {
			t.Errorf("neg[%d] %s %s reddedilmeli", i, n.op, n.req)
		}
	}
	positives := []struct {
		op  string
		req string
	}{
		{OpMailPreflight, `{}`},
		{OpMailStatus, `{}`},
		{OpMailDomainList, `{"limit":10}`},
		{OpMailDomainCreate, `{"domain":"mail.example.com"}`},
		{OpMailBoxList, `{"domain":"mail.example.com","limit":5}`},
		{OpMailBoxCreate, `{"domain":"mail.example.com","local":"ali"}`},
		{OpMailBoxDelete, `{"mailbox":"ali@mail.example.com"}`},
		{OpMailAliasCreate, `{"alias":"i@mail.example.com","target":"a@mail.example.com"}`},
		{OpMailQuotaSet, `{"mailbox":"ali@mail.example.com","bytes":536870912}`},
		{OpMailQueueList, `{}`},
		{OpMailLimitsGet, `{}`},
		{OpMailLimitsSet, `{"limits":{"message_size_limit":20971520,"anvil_rate_time_unit":300}}`},
	}
	for i, p := range positives {
		req := &Request{V: ProtocolV1, Operation: p.op, RequestID: "pos", Params: []byte(p.req)}
		if _, err := validateRequest(req); err != nil {
			t.Errorf("pos[%d] %s %s reddedildi: %v", i, p.op, p.req, err)
		}
	}
}

// Runner bağlıyken yürütme (happy) + runner hata kategori eşlemesi:
// ErrSyntaxCheck → executor_error (kriter 4; ortam-durumu redleri istek
// geçerli durum uygunsuz demektir), ErrInvalidDomain → invalid_params,
// ErrUnavailable → unsupported_operation.
func TestMailFamilyExecuteAndErrorMapping(t *testing.T) {
	srv := NewServer(Config{
		AllowedUIDs: []uint32{uint32(os.Getuid())},
		Mail:        &fakeMailRunner{},
	})
	happy := []struct {
		op   string
		req  string
		want string
	}{
		{OpMailPreflight, `{}`, StatusSucceeded},
		{OpMailStatus, `{}`, StatusSucceeded},
		{OpMailDomainList, `{}`, StatusSucceeded},
		{OpMailDomainCreate, `{"domain":"mail.example.com"}`, StatusSucceeded},
		{OpMailBoxCreate, `{"domain":"mail.example.com","local":"ali"}`, StatusSucceeded},
		{OpMailBoxDelete, `{"mailbox":"ali@mail.example.com"}`, StatusSucceeded},
		{OpMailAliasList, `{}`, StatusSucceeded},
		{OpMailQuotaSet, `{"mailbox":"ali@mail.example.com","bytes":536870912}`, StatusSucceeded},
		{OpMailConfigCheck, `{}`, StatusSucceeded},
		{OpMailQueueList, `{}`, StatusSucceeded},
		{OpMailLimitsGet, `{}`, StatusSucceeded},
		{OpMailLimitsSet, `{"limits":{"message_size_limit":20971520}}`, StatusSucceeded},
	}
	for i, h := range happy {
		req := &Request{V: ProtocolV1, Operation: h.op, RequestID: "h", Params: []byte(h.req)}
		call, verr := validateRequest(req)
		if verr != nil {
			t.Fatalf("pos[%d] %s şema reddedildi: %v", i, h.op, verr)
		}
		res := srv.execute(req, call)
		if res.Status != h.want {
			t.Fatalf("pos[%d] %s status = %s (%s)", i, h.op, res.Status, res.Output)
		}
	}

	srvFail := NewServer(Config{
		AllowedUIDs: []uint32{uint32(os.Getuid())},
		Mail:        &fakeMailRunner{err: mailops.ErrSyntaxCheck},
	})
	req := &Request{V: ProtocolV1, Operation: OpMailDomainCreate, RequestID: "f",
		Params: []byte(`{"domain":"mail.example.com"}`)}
	call, _ := validateRequest(req)
	res := srvFail.execute(req, call)
	if res.Status != StatusFailed || res.ErrorCategory != CatExecutorFailed {
		t.Fatalf("ErrSyntaxCheck kategorisi = %s/%s, want failed/executor_error",
			res.Status, res.ErrorCategory)
	}

	srvBad := NewServer(Config{
		AllowedUIDs: []uint32{uint32(os.Getuid())},
		Mail:        &fakeMailRunner{err: errors.Join(mailops.ErrInvalidDomain)},
	})
	res = srvBad.execute(req, call)
	if res.Status != StatusRejected || res.ErrorCategory != CatInvalidParams {
		t.Fatalf("ErrInvalidDomain kategorisi = %s/%s, want rejected/invalid_params",
			res.Status, res.ErrorCategory)
	}

	srvUn := NewServer(Config{
		AllowedUIDs: []uint32{uint32(os.Getuid())},
		Mail:        &fakeMailRunner{err: mailops.ErrUnavailable},
	})
	res = srvUn.execute(req, call)
	if res.Status != StatusRejected || res.ErrorCategory != CatUnsupportedOp {
		t.Fatalf("ErrUnavailable kategorisi = %s/%s, want rejected/unsupported_operation",
			res.Status, res.ErrorCategory)
	}
}
