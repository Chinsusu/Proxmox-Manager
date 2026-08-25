# systemd units (P0-12)

Triển khai `vmf-api`/`vmf-worker` qua systemd, theo Initial Deployment
(docs/10 mục 3) và release-symlink layout mà `scripts/deploy-release.sh`
tạo ra:

```text
/opt/vmf/
├── releases/
│   ├── v0.1.0/
│   │   ├── bin/{vmf-api,vmf-worker,vmf}
│   │   ├── migrations/
│   │   └── ...
│   └── v0.1.1/
└── current -> releases/v0.1.1        # symlink, scripts/rollback.sh doi symlink nay
/etc/vm-factory/
└── config.yaml                        # tu configs/vm-factory.example.yaml, KHONG chua secret
/run/credentials/                      # tmpfs, mat sau reboot, chi root+vmf doc duoc — khop
                                        # duong dan *_file trong configs/vm-factory.example.yaml
├── postgres-dsn                       # 0400, owner vmf
├── pve-token                          # 0400
├── identity-hmac-key                  # 0400
├── jwt-public-key.pem                 # 0400 (vmf-api)
└── pgw-token                          # 0400 (vmf-worker, hien chua dung — P0-04)
```

`/run/credentials` la tmpfs — phai populate lai sau MOI lan reboot (vd
qua unit `tmpfiles.d`/`systemd-creds` hoac script provisioning rieng
cua ha tang, ngoai pham vi P0). KHONG dung /etc de luu secret dang
plaintext lau dai.

## Cài đặt lần đầu

```bash
useradd --system --no-create-home --shell /usr/sbin/nologin vmf
mkdir -p /opt/vmf/releases /etc/vm-factory
install -d -m 0750 -o vmf -g vmf /run/credentials

# giai nen release (xem scripts/build-release.sh/scripts/deploy-release.sh)
scripts/deploy-release.sh vmf-release-v0.1.0.tar.gz

# credential — KHONG commit gia tri that vao git, day file thu cong hoac
# qua secret manager rieng cua ha tang (Phan II muc 15: "secret qua file").
install -m 0400 -o vmf -g vmf /path/to/real/dsn        /run/credentials/postgres-dsn
install -m 0400 -o vmf -g vmf /path/to/real/pve-token  /run/credentials/pve-token
install -m 0400 -o vmf -g vmf /path/to/real/hmac-key   /run/credentials/identity-hmac-key
install -m 0400 -o vmf -g vmf /path/to/real/jwt-pub.pem /run/credentials/jwt-public-key.pem

cp configs/vm-factory.example.yaml /etc/vm-factory/config.yaml
# /etc/vm-factory/config.yaml mac dinh *_file da tro dung /run/credentials/*

cp deploy/systemd/vmf-api.service deploy/systemd/vmf-worker.service /etc/systemd/system/
systemctl daemon-reload
systemctl enable --now vmf-api vmf-worker
```

## Xoay vòng credential

Theo docs/10 mục 7 (Credential Rotation): deploy file mới CẠNH file cũ
với tên khác, sửa `config.yaml` trỏ sang file mới, `systemctl reload`
(hoặc `restart` nếu service không hỗ trợ SIGHUP reload — cả hai unit ở
đây dùng `Type=simple`, không tự reload config đang chạy, nên bước này
thực chất là `restart`), verify request thành công, rồi mới xoá file cũ.

## Rollback

`scripts/rollback.sh` đổi symlink `/opt/vmf/current` sang release
trước đó rồi `systemctl restart vmf-api vmf-worker` — KHÔNG tự động
rollback migration DB (migration down có thể mất dữ liệu, luôn là
quyết định thủ công có review, theo docs/10 mục 12 "Change Management").
