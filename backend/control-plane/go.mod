module github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane

go 1.27.3

require (
	github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/gen/proto v0.0.0-00010101000000-000000000000
	github.com/go-chi/chi/v5 v5.5.0
	github.com/jackc/pgx/v5 v5.12.0
	golang.org/x/crypto v0.58.0
	google.golang.org/grpc v1.85.0
	google.golang.org/protobuf v1.36.12
)

require (
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.3 // indirect
	golang.org/x/net v0.60.0 // indirect
	golang.org/x/sync v0.24.0 // indirect
	golang.org/x/sys v0.49.0 // indirect
	golang.org/x/text v0.43.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260817212433-ac3dfec99bb1 // indirect
)

replace github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/gen/proto => ../../gen/proto
