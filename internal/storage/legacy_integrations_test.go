package storage

import "testing"

func TestLegacyIntegrationCancellationRejectsPromotionAndPreservesRecord(t *testing.T) {
	s, ctx := newTestStore(t)
	board, err := s.DefaultBoard(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"ready", "promoting", "cleanup_required", "promoted", "failed"} {
		_, err := s.db.ExecContext(ctx, `insert into integration_runs(public_id,board_id,state,repository_root,common_dir,worktree_path,branch_name,source_branch,source_sha,harness,token_hash,prompt,created_at,updated_at) values(?,?,?,'/repo',?,'/candidate','candidate','main','source','pi','hash','historical prompt',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`, state, board.ID, state, "/repo/.git/"+state)
		if err != nil {
			t.Fatal(err)
		}
		err = s.CancelIntegrationRun(ctx, state)
		if (err == nil) != (state == "ready") {
			t.Fatalf("state=%s error=%v", state, err)
		}
		old, err := s.IntegrationRunByPublicID(ctx, state)
		want := state
		if state == "ready" {
			want = "cancelled"
		}
		if err != nil || old.State != want || old.Prompt != "historical prompt" {
			t.Fatalf("record=%+v err=%v", old, err)
		}
	}
}
