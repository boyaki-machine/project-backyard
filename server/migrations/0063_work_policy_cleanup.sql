-- +goose Up
ALTER TABLE work_session ADD COLUMN policy jsonb NOT NULL DEFAULT '{}';
ALTER TABLE work_session ADD COLUMN time_missing boolean NOT NULL DEFAULT false;
ALTER TABLE work_allocation ADD COLUMN change_metrics jsonb;
CREATE TABLE ticket_work_base (
 ticket_id char(26) COLLATE "C" PRIMARY KEY REFERENCES ticket(id) ON DELETE CASCADE,
 sessions_count bigint NOT NULL DEFAULT 0,
 incomplete_count bigint NOT NULL DEFAULT 0,
 tokens_missing bigint NOT NULL DEFAULT 0,
 time_missing bigint NOT NULL DEFAULT 0,
 active_ms double precision NOT NULL DEFAULT 0,
 elapsed_ms double precision NOT NULL DEFAULT 0,
 waiting_ms double precision NOT NULL DEFAULT 0,
 tokens double precision NOT NULL DEFAULT 0,
 point_inputs jsonb NOT NULL DEFAULT '[]'
);
CREATE TABLE work_archive (
 id char(26) COLLATE "C" PRIMARY KEY,
 project_id char(26) COLLATE "C" NOT NULL REFERENCES project(id) ON DELETE CASCADE,
 actor_id char(26) COLLATE "C" REFERENCES actor(id) ON DELETE SET NULL,
 ticket_ids jsonb NOT NULL,
 time_missing boolean NOT NULL,
 cleaned_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_work_archive_project ON work_archive(project_id);
CREATE TABLE work_replay (
 project_id char(26) COLLATE "C" NOT NULL REFERENCES project(id) ON DELETE CASCADE,
 actor_id char(26) COLLATE "C" NOT NULL REFERENCES actor(id) ON DELETE CASCADE,
 request_id text NOT NULL,
 session_id char(26) COLLATE "C" NOT NULL REFERENCES work_archive(id) ON DELETE CASCADE,
 digest text NOT NULL,
 PRIMARY KEY(project_id,actor_id,request_id)
);

UPDATE document SET body_md=body_md || '
## 作業実績の記録

**Agentは承認後の実作業開始時にpb_start_workを呼び、人への返答待ちをpause、再開をresume、終了をfinishで記録する。** 終了時に実績時間・Pointの保存結果を確認し、作業記録IDを完了レポートへ指定する。計測不能なら対象と理由を明示し、不明を0で補わない。

算出方法はプロジェクトマネジメントノートの「実績」を章指定で取得する：pb_get_doc(path="project-management-note", section="実績")。PBが共通契約で算出するため、Agentごとに式を変更しない。
', version=version+1,updated_at=now()
WHERE is_template AND template_key='default' AND slug='rules';

UPDATE document SET body_md=replace(body_md,'### 見積','**共通計測の契約。** 次のJSONブロックをこの章の正本として文書編集画面から管理する。開始時に文書版と契約を固定する。時間は取得元付きのランタイム稼働時間がある場合に優先し、なければPB計時（開始から終了のうち申告した待機を除く）を用いる。純粋な思考時間ではない。

```pb-effort-policy
{"version":"actual-v0","point_formula":"change-volume-v0","time_source":"reported_or_pb","require_registration":true}
```

Pointは変更量ベースのactual-v0。規模＝コード行数＋試験×0.5＋文書×0.7＋その他×0.3、指標E＝log2(1＋規模)＋0.5log2(1＋ファイル数)＋0.5(ブランチ数−1)＋0.5教訓数。Eの境界4.3/7/9.05/10.85/12.9/15.9未満で1/2/3/5/8/13、それ以上は21。変更量0や必要な入力が欠測なら未算出とし、時間からPointを代用しない。

終了時のchange_metricsは各チケット全体の累積値と取得元を報告する。セッションごとの値を単純加算しない。生成API型・store/gen・通知・ロックファイル・VERSION・履歴・distを除く。試験、コード、文書、その他を分ける。別の作業と区別できなければ省略し、未算出とする。トークンは取得できた値のみを報告する。手入力と意図的な空欄は保護する。不完全な記録は参考値として区別する。

### 見積'),version=version+1,updated_at=now()
WHERE is_template AND template_key='default' AND slug='project-management-note';
