// Static baselines from reviewed 021, 023, 024 and current 004/006/007/008 SQL. Never derive
// expected procedure definitions from the database being verified. See 027 and B2 §7.3.
package operations

const paymentRoleMigrationSHA256 = "757721a8559ce6710972759aaa1eb4bbd1a4af6d0b1a058d45f69c121643323d"
const walletSourcesRoleMigrationSHA256 = "67e8289f1e550ea826f4890b1b046e2d10d502537b764e0441d2cb0ae3f5eb4a"
const pairedSourcesRoleMigrationSHA256 = "9f4785bcc8d912f04e25d37c3d359b73f112f031f95989f089b9d255eeea357f"
const refundBucketsRoleMigrationSHA256 = "fe82ab8febcd62c5c529d5c93bd134d1a45cd9839a5bd75dbee6fada39b51938"
const coinRatesRoleMigrationSHA256 = "e723be6de772528606cb0178d58d34e914a234d5c6f02ae7914ead13547dfbd2"
const coinRatePaymentFundingJournalSHA256 = "3a70171598ed46380396ef91fe3da9d42a837f3cb63c5e0d88cf8943dad03bad"
const coinRatePaymentSandboxJournalSHA256 = "2cb027adfa1e356e6a81b669c981173ec7fda211e6680810943b378ec76b4d97"
const legacyWalletEventValidSHA256 = "07545d756fa2c25d26fe4bf6d93777330e3839e179877298168ce3d502342b6f"
const virtualCoinPaymentFundingJournalSHA256 = "10ea84ec2f7bd409044fd8febb2fa90735f0946509e2567ef3b102d7023dd184"
const virtualCoinWalletOpeningJournalSHA256 = "45f298162c92d5a825a89a06a807dfdfc4e9822a3bc7944f49cf4fdf1339c6d6"
const virtualCoinPaymentSandboxJournalSHA256 = "0b39ad17c94be382e2086f8812a9a83f031e8f8d3c22819bc957eb6629eeecb2"
const pairedPaymentFundingJournalSHA256 = "b50881f5b28ccdc60289207568074af25ba6349bcb5faf2237a75d9b8a10fba5"
const pairedWalletOpeningJournalSHA256 = "0b1159b9e46cab9df1928381fcf9892ab9b0b3b9ba61dcf350d96168206eb633"
const pairedPaymentSandboxJournalSHA256 = "a184d892c18045ae549b0d444caa98c2901baaaa0f5c9edc42facc0d5fe211bc"
const pairedWalletRefundSHA256 = "98e27d03adbc421ea6780cf93418a58e171cc836ccae0c7ab6de7099291af21d"
const pairedFundingFactGuardSHA256 = "edbba29ccb46eb67a22bdf2ad8f740c108a29854cee391d2260c8f05d0bca21e"
const pairedFundingJournalSHA256 = "8dd76eb25cb0f63ebcbb6f1c7aeb26bdda1af0118bd9227d2052959aac4ccaf7"
const pairedPaymentVoucherJournalSHA256 = "2e8bb37f9058fba7f12e47b1b18b91aef071f286c71188515998f18bcef599d9"
const refundLotBalanceGuardSHA256 = "595513e84728c234340a8bcf658e6c48d0a10564fb38c2238d6be836f629ffc7"
const refundWalletEventValidSHA256 = "9711ab26b5b37e43861c3ad97005204daa252b31290e0f60502530f69614487b"
const refundWalletJournalValidSHA256 = "45720a2bc80e45a14bd3613b102c0c71d0e2781a96f2babd8cfec1f803fe3151"
const refundWalletLotValidSHA256 = "ae9495a85b0a5fe45a993a0a3e5727b391e9c6bdc1e383da1c2487d823b08c45"
const refundWalletAccountValidSHA256 = "4802c4413197b809c9490d4ca8b658ea9829e2d7d795e24d12b16fd4c0d5af89"
const refundFundingJournalValidSHA256 = "fd5d9858d47a51bcacf549bef0ba80a6f53e28c7a503e864e9ad0cb0bc6dd954"
const refundFundingFactGuardSHA256 = "1e344db9c567f4d3107d761070786474d28004c4f03ec1380d294d5b2b7ea138"
const refundWalletSourceFactGuardSHA256 = "eb4174fdc7f95a47566e0f59b84a3187ef9414ae784e05546490a16909658afc"

var paymentFunctionSpecs = map[string]paymentFunctionSpec{
	"funding_fact_guard":               {"funding_fact_guard", "", "", "2279", "plpgsql", "v", "b82f809ec5663cfa681702e7044541207a08b3d167c051f4933cf2d152412885", nil, true},
	"funding_journal_valid":            {"funding_journal_valid", "text", "25", "16", "plpgsql", "s", "be1dfa836c1a0823e56d4f138b6bd6212a6b86f4babe7934481053c2ee87b30b", []string{"journal_ref"}, true},
	"payment_audit_complete":           {"payment_audit_complete", "", "", "2279", "plpgsql", "v", "7715e434d176fff2058d3fe01429998fe3acada5ebc1a6cbb1dbf1d8b9c4d198", nil, false},
	"payment_audit_guard":              {"payment_audit_guard", "", "", "2279", "plpgsql", "v", "9e9f4d17a8ce9e4b2122a178bfdfde561b4cbadc439295337c40d27acd9ee79e", nil, false},
	"payment_backoff":                  {"payment_backoff", "integer", "23", "1186", "sql", "i", "92b18995264e82a5b2fa925287e34f28afecbeef06a3d3064ca1525bcdab6bf1", []string{"n"}, true},
	"payment_cancel_insert":            {"payment_cancel_insert", "", "", "2279", "plpgsql", "v", "6971b0ac4f1ec4880253aae4208199663b7a4c7c2a6075919403c0095fade264", nil, false},
	"payment_capture_evidence_valid":   {"payment_capture_evidence_valid", "text", "25", "16", "plpgsql", "s", "1727c7e2a9f5bef28df74806e3e05b7dd85610cfa6a3daa9b3e6f768657f6c94", []string{"evidence_ref"}, true},
	"payment_capture_generation":       {"payment_capture_generation", "", "", "2279", "plpgsql", "v", "0be0d551dc5b3df3b764a4194c7ff6b87a63b3a60b2dfb68a18a49967d8f97fd", nil, true},
	"payment_capture_source_valid":     {"payment_capture_source_valid", "text", "25", "16", "sql", "s", "0f9a4b3bea3fcb1058bfbd2aa7159ac3e739c1460ce1335e89519bb3ce1baa71", []string{"capture_ref"}, true},
	"payment_check_human":              {"payment_check_human", "text", "25", "2278", "plpgsql", "v", "b22fbda09e8bf03f8da34efaef05f4d0bd9b75e6412a077ed43cd68956d3c07f", []string{"operation_ref"}, false},
	"payment_evidence_complete":        {"payment_evidence_complete", "", "", "2279", "plpgsql", "v", "e2ae776999371ca7488e54ab0b604711c26cc2093d6c10e59ca47cf6f1cdc9ea", nil, true},
	"payment_evidence_insert":          {"payment_evidence_insert", "", "", "2279", "plpgsql", "v", "aa3ff70fdc9ea68582ca0bc3c485d8f7880edb78153e88e231d581cbe00ec0fe", nil, true},
	"payment_extra_funding_complete":   {"payment_extra_funding_complete", "", "", "2279", "plpgsql", "v", "545c0814751286aa63c9e11a0ebe13d7ad1521b5db7e96ef556c314b5cf01394", nil, true},
	"payment_frame":                    {"payment_frame", "text[]", "1009", "17", "plpgsql", "i", "169bab911cd89f43cfe4a6f5412343c718157e6aec595c9737481eeba1c1e607", []string{"parts"}, true},
	"payment_human_complete":           {"payment_human_complete", "", "", "2279", "plpgsql", "v", "2b2dc34518fca823db1d3f697ee1989b4e980ab2f61681a922eb0ee716444bac", nil, false},
	"payment_identity_valid":           {"payment_identity_valid", "text", "25", "16", "sql", "s", "4c6aaedecc39b077dad162dfe0b38fe358f1fb7fcc6f56eb04dca7324562b395", []string{"identity_ref"}, true},
	"payment_funding_journal_valid":    {"payment_funding_journal_valid", "text", "25", "16", "sql", "s", "b70673e2bc16b7c565d1eed6f8def458361c1baf518649f221fa5f7a06900886", []string{"journal_ref"}, false},
	"payment_voucher_journal_valid":    {"payment_voucher_journal_valid", "text", "25", "16", "sql", "s", "2e8bb37f9058fba7f12e47b1b18b91aef071f286c71188515998f18bcef599d9", []string{"journal_ref"}, false},
	"payment_observation_insert":       {"payment_observation_insert", "", "", "2279", "plpgsql", "v", "432bb882429d8be652aca759a5bb22c8b6c31a24c3afc9a3e3316270f5cfbf21", nil, true},
	"payment_order_insert":             {"payment_order_insert", "", "", "2279", "plpgsql", "v", "0c50ffaa9904a96fa786b06fda603e833e9d58243a8770a2f3b3c407991ee27c", nil, false},
	"payment_original_complete":        {"payment_original_complete", "", "", "2279", "plpgsql", "v", "fae586952f14002b1c008b15c8611a683ee1521eed21df468f0ecdd4474f71fc", nil, true},
	"payment_original_insert":          {"payment_original_insert", "", "", "2279", "plpgsql", "v", "07cb0cfd9757aa7d8797e295c87900f9a22472bf5c5680ed2a966923ce97a6d4", nil, true},
	"payment_outbox_complete":          {"payment_outbox_complete", "", "", "2279", "plpgsql", "v", "e75ae691ba196f74c31746280076c764628006ea1ba63facb894d5dafeffcd80", nil, true},
	"payment_outbox_insert":            {"payment_outbox_insert", "", "", "2279", "plpgsql", "v", "df9b6b2dcdc35b603398d83d5901ca95c6b44855e1b804a150cdfbbf3d832027", nil, true},
	"payment_outbox_transition":        {"payment_outbox_transition", "", "", "2279", "plpgsql", "v", "f3e4f27049a87365ff8d52c5fddba1882c33ce126d7b782a724c942a32a8e002", nil, true},
	"payment_policy_guard":             {"payment_policy_guard", "", "", "2279", "plpgsql", "v", "d26bd59207b1582ce867b8c68b77d1dd3b57992e14effa04f0dc0367b0b22c8b", nil, false},
	"payment_processing_guard":         {"payment_processing_guard", "", "", "2279", "plpgsql", "v", "ff55c3fe8b9238410125977886c5c8f5fb327a80ced6cc3dcf5fa0afcd5d217b", nil, true},
	"payment_query_attempt_insert":     {"payment_query_attempt_insert", "", "", "2279", "plpgsql", "v", "644756271566bdf8bafebeee764b0fd473a339fbf0e0b67a3593698191fc28f1", nil, true},
	"payment_ref":                      {"payment_ref", "text", "25", "16", "sql", "i", "6da90a178460db1d6ff7f1accb4293a80ebcbcddd7610ca5b669f2c30af86446", []string{"v"}, true},
	"payment_require_rc":               {"payment_require_rc", "", "", "2278", "plpgsql", "v", "1bb2aabf288425b8b425ecd13714b460bd4144b4ed4e50e4c251dca65f979ed5", nil, true},
	"payment_review_insert":            {"payment_review_insert", "", "", "2279", "plpgsql", "v", "669da72ffdef4c8fe81aead1848631bc4889bf4c4e48931e08a92ca5ed9024e9", nil, true},
	"payment_revision_insert":          {"payment_revision_insert", "", "", "2279", "plpgsql", "v", "b7d253f9863f0a23635e600ff1db48cdc3994a6b856cab7098758a581f940551", nil, true},
	"payment_sandbox_journal_valid":    {"payment_sandbox_journal_valid", "text", "25", "16", "sql", "s", "005048b5cfc28a9ed69e7b10b4751403882fb089fe7c5ac4f533e70217672fb2", []string{"journal_ref"}, true},
	"payment_sku_change_apply":         {"payment_sku_change_apply", "", "", "2279", "plpgsql", "v", "a55c20b6d9a92b301a3526af1489bacff997c26a0d8b8e57dee4e6d255b40388", nil, false},
	"payment_sku_change_insert":        {"payment_sku_change_insert", "", "", "2279", "plpgsql", "v", "e1a1d9be009c16103b7a7dfe94941be87543cca9edea2cdcc0f9bd0431d3d34c", nil, false},
	"payment_sku_root_guard":           {"payment_sku_root_guard", "", "", "2279", "plpgsql", "v", "bfe316337d46e712a49b60e558501a21c0c13f3445bbd429f3bc6a488f1c5a92", nil, false},
	"payment_sku_version_insert":       {"payment_sku_version_insert", "", "", "2279", "plpgsql", "v", "0d5da9a59088f684eb9332f989c3c6fde3cb6747a2bede6ea24c632ee6ba33c0", nil, false},
	"payment_source_complete":          {"payment_source_complete", "", "", "2279", "plpgsql", "v", "34d7a5aa1836614b1e6a8fd3b79774af723e1f463d31b60ce05b430837161504", nil, true},
	"payment_source_writer":            {"payment_source_writer", "", "", "2279", "plpgsql", "v", "4a43ef8334d2b670e0c2f95ca0e5a07dadc183ded74bf83ad0da1b85fddb45f7", nil, true},
	"payment_time":                     {"payment_time", "timestamp with time zone", "1184", "25", "sql", "s", "3144aa3c5c4e12c998d5f8427045c162417486a5a4820646d50e20de21bc8bc0", []string{"v"}, true},
	"payment_worker_account_guard":     {"payment_worker_account_guard", "", "", "2279", "plpgsql", "v", "25955998fbab1f5c7185fcddb665a559af924af453f42c76dca8a70cac1fda8d", nil, true},
	"payment_worker_balance_complete":  {"payment_worker_balance_complete", "", "", "2279", "plpgsql", "v", "54984088cbfbbb8006672c5c7db4b345a386dd799c2dcb93e9021b3cfa6e446c", nil, true},
	"payment_worker_line_guard":        {"payment_worker_line_guard", "", "", "2279", "plpgsql", "v", "a79276dc4fe5465606d9b6fb754ff2bfccb6ea4766f89621d983896c72d74966", nil, true},
	"payment_worker_tenant_lock_only":  {"payment_worker_tenant_lock_only", "", "", "2279", "plpgsql", "v", "2234631940b76da251687b6873076687c4322bffb316126d5a06680459240a06", nil, true},
	"wallet_opening_journal_valid":     {"wallet_opening_journal_valid", "text", "25", "16", "sql", "s", "e518940c681bb8cd58d437596e5666c5a8789bb985dc3b3d79bced612a3bb734", []string{"journal_ref"}, false},
	"wallet_lot_balance_guard":         {"wallet_lot_balance_guard", "", "", "2279", "plpgsql", "v", "425c2cefb7893ba51aff99d1e5692b4dc860fef820798095fb74bd0381a49839", nil, true},
	"wallet_lot_event_stamp":           {"wallet_lot_event_stamp", "", "", "2279", "plpgsql", "v", "287edc8686fd1dc72209fca494ab1f9c7b7710bd7ef94de7f0ff50e9b4d3c437", nil, true},
	"wallet_lot_valid":                 {"wallet_lot_valid", "text", "25", "16", "sql", "s", "040f32222d476d0f27143f7eb5502d154720214d9483a5a4836e98de3e5e38cf", []string{"lot_ref"}, true},
	"wallet_event_valid":               {"wallet_event_valid", "text", "25", "16", "plpgsql", "s", "cf1d5e4ba13a6427cd1ced2e0cbc4c75d082999ca427098e01bffd9b05d5b3c7", []string{"event_ref"}, true},
	"wallet_reservation_fifo_guard":    {"wallet_reservation_fifo_guard", "", "", "2279", "plpgsql", "v", "6f966a203aa31f11b0bb1eae7fd4a95dd17976cd9eefedd1e6981ae7e1cd4d77", nil, false},
	"wallet_reservation_valid":         {"wallet_reservation_valid", "text", "25", "16", "sql", "s", "af87dce7e1bdd19d2fae3dbeb267da2e767952a98af8f7f15b34ff513c3bf541", []string{"res_ref"}, false},
	"wallet_settlement_valid":          {"wallet_settlement_valid", "text", "25", "16", "sql", "s", "4130762d279e5ab9fb5e9ae30ae38dd9bc586e7dfa1a0ae0c7157b307b2ec87d", []string{"set_ref"}, false},
	"wallet_refund_valid":              {"wallet_refund_valid", "text", "25", "16", "sql", "s", "5e69019f7ec42e62009bba561358f2350c7c63a40cfda4bc6fe1b69333ee255a", []string{"ref_ref"}, false},
	"wallet_journal_valid":             {"wallet_journal_valid", "text", "25", "16", "sql", "s", "9a47fbf42d77335834e907f868a158798db1a200e74d2a4206d1bcf9cf7baf8b", []string{"journal_ref"}, true},
	"wallet_account_valid":             {"wallet_account_valid", "text", "25", "16", "sql", "s", "78eb07c7c877bea6280952fd0202aaa70daa82a01511219019e2a9c0208345ee", []string{"account_ref"}, true},
	"wallet_source_fact_guard":         {"wallet_source_fact_guard", "", "", "2279", "plpgsql", "v", "48d05ddcdebd596a001fb55973cae00ceb0620a5220ce2124ccdcda50b8f7f39", nil, true},
	"wallet_single_operator":           {"wallet_single_operator", "", "", "16", "sql", "s", "0bcb527d011a0e1e1506fc9c7e2b96e732d817cd2fd97f68f59be9e57c02827d", nil, false},
	"wallet_operator_role_valid":       {"wallet_operator_role_valid", "text,text", "25 25", "16", "sql", "s", "f4cffcfe1b6ae7f05608b403c6e94a05e60fa634f304d8e8f8439323ff1d6154", []string{"actor", "required"}, false},
	"wallet_approval_snapshot_valid":   {"wallet_approval_snapshot_valid", "text,text,text", "25 25 25", "16", "plpgsql", "s", "2adb76e993a92cd96c0c807a35fe48e745e272c3cee4fd046b724d7ec12dabd2", []string{"requester", "approver", "requested_by"}, false},
	"wallet_credit_pair_valid":         {"wallet_credit_pair_valid", "text", "25", "16", "sql", "s", "38de1c8c67e76d46540e9310e7b00316721a093398901e0214630342206450bf", []string{"credit_ref"}, false},
	"wallet_request_guard":             {"wallet_request_guard", "", "", "2279", "plpgsql", "v", "1b8b259670efbb4ccd2fe382e0a811e7f454d5a1a079ebc0f1f12d721700a674", nil, false},
	"wallet_cash_refund_journal_valid": {"wallet_cash_refund_journal_valid", "text", "25", "16", "sql", "s", "f8049d29b2bde1f531580a7cfcb5d5e454318e9fc610f8b81fadd5bd4614e90e", []string{"journal_ref"}, false},
	"wallet_cash_refund_order_valid":   {"wallet_cash_refund_order_valid", "text", "25", "16", "sql", "s", "5fb55b8e58cb0a68495d7366ecd648838368070f5e4f6052b528a9e57752f6f1", []string{"order_ref"}, false},
}
