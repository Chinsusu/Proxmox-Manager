// Package domain chứa aggregate/entity/value object cốt lõi (VM instance,
// template, job, IPAM, egress binding...) theo Phần II mục 5 và Phần VI.
// Không phụ thuộc trực tiếp SDK Proxmox/PGW — chỉ dùng qua port interface.
// Triển khai ở epic P0-01.
package domain
