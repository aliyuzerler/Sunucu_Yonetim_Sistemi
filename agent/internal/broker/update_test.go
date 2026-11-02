//go:build unix

// Faz 15 broker katmanı testleri (üç-katman disiplinin update karşılığı —
// ADR-0020):
//   - Kapı VARSAYILAN KAPALI: Config.Update nil → tipli unsupported.
//   - Strict şema negatifleri: payload'da yol/hedef ALANI YOK (path
//     enjeksiyonu runner'a ASLA ulaşamaz), bozuk UUID/sha256/imza reddi.
//   - Yürütme: fake runner ile happy yolu + hata kategorisi eşlemesi
//     (ErrTrustKeyMissing/ErrBadSignature → invalid_params; ErrHashMismatch →
//     executor_error; ErrHealthCheck → executor_error).

package broker

import (
        "context"
        "encoding/json"
        "os"
        "strings"
        "testing"

        "github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/updateops"
)

type fakeUpdateRunner struct {
        err        error
        stageHit   bool
        rollbackOK bool
        lastStage  updateops.StagePayload
}

func (f *fakeUpdateRunner) Stage(_ context.Context, p updateops.StagePayload) (updateops.StageResult, error) {
        f.stageHit = true
        f.lastStage = p
        if f.err != nil {
                return updateops.StageResult{}, f.err
        }
        return updateops.StageResult{State: "staged", Version: p.Version, Artifact: "staged-abcd",
                SHA256: p.SHA256, HealthCheck: "ok", HealthOut: "server-agent version " + p.Version}, nil
}

func (f *fakeUpdateRunner) Status(context.Context) (updateops.StatusResult, error) {
        return updateops.StatusResult{ActiveVersion: "server-agent version 0.15.0", Staged: true,
                StagedVersion: "0.16.0-rc1", RollbackReady: true, IncomingFiles: 1, UpdateEnabled: true}, f.err
}

func (f *fakeUpdateRunner) Rollback(context.Context) (updateops.RollbackResult, error) {
        return updateops.RollbackResult{State: "rolled_back", HealthCheck: "ok"}, f.err
}

var _ UpdateRunner = (*fakeUpdateRunner)(nil)

// Kapı VARSAYILAN KAPALI: Config.Update nil → her update op'u tipli unsupported.
func TestUpdateFamilyDisabledByDefault(t *testing.T) {
        srv := NewServer(Config{AllowedUIDs: []uint32{uint32(os.Getuid())}})
        gate := []struct {
                op  string
                req string
        }{
                {OpUpdateStage, `{"update_id":"0b7fd4d2-1234-4c2b-9a53-2b6f8f8a1111","version":"0.16.0-rc1","sha256":"` + strings.Repeat("ab", 32) + `","signature":"` + strings.Repeat("A", 88) + `"}`},
                {OpUpdateStatus, `{}`},
                {OpUpdateRollback, `{}`},
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

// Strict şema negatifleri: yol/hedef enjeksiyonu + bozuk kimlikler.
func TestUpdateFamilyValidation(t *testing.T) {
        uid := "0b7fd4d2-1234-4c2b-9a53-2b6f8f8a1111"
        sha := strings.Repeat("ab", 32)
        sig := strings.Repeat("A", 88)
        negatives := []struct {
                op  string
                req string
        }{
                {OpUpdateStage, `{"update_id":"zort","version":"0.16.0-rc1","sha256":"` + sha + `","signature":"` + sig + `"}`},
                {OpUpdateStage, `{"update_id":"` + uid + `","version":"x","sha256":"` + sha + `","signature":"` + sig + `"}`},
                {OpUpdateStage, `{"update_id":"` + uid + `","version":"0.16.0-rc1","sha256":"XYZ","signature":"` + sig + `"}`},
                {OpUpdateStage, `{"update_id":"` + uid + `","version":"0.16.0-rc1","sha256":"` + sha + `","signature":"kisa"}`},
                // Yol/hedef alanı payload'da YOK — fazla alan şema ihlali:
                {OpUpdateStage, `{"update_id":"` + uid + `","version":"0.16.0-rc1","sha256":"` + sha + `","signature":"` + sig + `","path":"/etc/shadow"}`},
                {OpUpdateStage, `{"update_id":"` + uid + `","version":"0.16.0-rc1","sha256":"` + sha + `","signature":"` + sig + `","target_bin":"/usr/bin/evil"}`},
                {OpUpdateStage, `{}`},
                {OpUpdateStatus, `{"target":"/etc"}`},
                {OpUpdateRollback, `{"x":1}`},
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
                {OpUpdateStage, `{"update_id":"` + uid + `","version":"0.16.0-rc1","sha256":"` + sha + `","signature":"` + sig + `"}`},
                {OpUpdateStatus, `{}`},
                {OpUpdateStatus, ``},
                {OpUpdateRollback, `{}`},
        }
        for i, p := range positives {
                req := &Request{V: ProtocolV1, Operation: p.op, RequestID: "pos", Params: []byte(p.req)}
                if _, err := validateRequest(req); err != nil {
                        t.Errorf("pos[%d] %s %s reddedilmemeli: %v", i, p.op, p.req, err)
                }
        }
}

// Yürütme + hata kategorisi eşlemesi.
func TestUpdateFamilyExecution(t *testing.T) {
        uid := "0b7fd4d2-1234-4c2b-9a53-2b6f8f8a1111"
        sha := strings.Repeat("ab", 32)
        sig := strings.Repeat("A", 88)
        stageReq := `{"update_id":"` + uid + `","version":"0.16.0-rc1","sha256":"` + sha + `","signature":"` + sig + `"}`

        runner := &fakeUpdateRunner{}
        srv := NewServer(Config{AllowedUIDs: []uint32{uint32(os.Getuid())}, Update: runner})

        // Happy stage.
        req := &Request{V: ProtocolV1, Operation: OpUpdateStage, RequestID: "s1", Params: []byte(stageReq)}
        call, verr := validateRequest(req)
        if verr != nil {
                t.Fatalf("şema: %v", verr)
        }
        res := srv.execute(req, call)
        if res.Status != StatusSucceeded {
                t.Fatalf("stage başarılı beklenir: %+v", res)
        }
        if !runner.stageHit || runner.lastStage.Version != "0.16.0-rc1" {
                t.Fatalf("runner çağrılmadı: %+v", runner.lastStage)
        }
        var got map[string]any
        if err := json.Unmarshal(res.Result, &got); err != nil || got["health_check"] != "ok" {
                t.Fatalf("health_check ok beklenir: %v %v", got, err)
        }

        // İmza hatası → tipli RED (fail-closed güven kategorisi).
        runner.err = updateops.ErrBadSignature
        res = srv.execute(req, call)
        if res.Status != StatusRejected || res.ErrorCategory != CatInvalidParams {
                t.Fatalf("ErrBadSignature → rejected/invalid_params beklenir: %+v", res)
        }
        // Hash uyuşmazlığı → executor_error (artifact indirme/okuma sonrası).
        runner.err = updateops.ErrHashMismatch
        res = srv.execute(req, call)
        if res.ErrorCategory != CatExecutorFailed {
                t.Fatalf("ErrHashMismatch → executor_error beklenir: %+v", res)
        }
        runner.err = nil

        // Status + rollback.
        reqS := &Request{V: ProtocolV1, Operation: OpUpdateStatus, RequestID: "s2", Params: []byte(`{}`)}
        callS, _ := validateRequest(reqS)
        res = srv.execute(reqS, callS)
        if res.Status != StatusSucceeded {
                t.Fatalf("status: %+v", res)
        }
        reqR := &Request{V: ProtocolV1, Operation: OpUpdateRollback, RequestID: "s3", Params: []byte(`{}`)}
        callR, _ := validateRequest(reqR)
        res = srv.execute(reqR, callR)
        if res.Status != StatusSucceeded {
                t.Fatalf("rollback: %+v", res)
        }
        var got2 map[string]any
        if err := json.Unmarshal(res.Result, &got2); err != nil || got2["state"] != "rolled_back" {
                t.Fatalf("rollback state: %v %v", got2, err)
        }
}
