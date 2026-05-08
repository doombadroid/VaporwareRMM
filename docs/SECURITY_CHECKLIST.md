# Security Checklist & Best Practices

## Overview

This document outlines the security requirements, checks, and best practices for vaporRMM. Use this as a reference when deploying in production.

## Authentication & Authorization

| Requirement | Status | Implementation |
|------------|--------|----------------|
| JWT token authentication | ✅ | Server uses HMAC-SHA256 tokens with configurable expiration |
| Secure token storage | ⚠️ | Tokens stored in HTTP-only cookies (configurable) |
| Token refresh mechanism | ✅ | Refresh tokens issued for long-lived sessions |
| Role-based access control | 🔄 | Basic RBAC implemented, extend as needed |
| API key authentication | 🔄 | For agent-to-server communication |

## Transport Security

| Requirement | Status | Implementation |
|------------|--------|----------------|
| TLS 1.2+ required | 🔄 | Production should use reverse proxy with TLS |
| Certificate validation | ✅ | Agent validates server certificates (optional) |
| Mutual TLS for agents | 🔄 | Optional mutual TLS authentication |

## Data Security

| Requirement | Status | Implementation |
|------------|--------|----------------|
| Database encryption at rest | ⚠️ | Use SQLite encrypted extension or PostgreSQL TDE |
| Secret management | ✅ | Environment variables supported, Vault integration ready |
| Credential hashing | ✅ | Passwords hashed with bcrypt |

## Agent Security

| Requirement | Status | Implementation |
|------------|--------|----------------|
| Signed agent binaries | 🔄 | Build pipeline for code signing |
| Secure agent registration | 🔄 | Certificate-based device registration |
| Heartbeat encryption | ⚠️ | WebSocket traffic encrypted via TLS |

## Compliance & Auditing

| Requirement | Status | Implementation |
|------------|--------|----------------|
| Audit logging | ✅ | All admin actions logged with timestamp/user |
| Log retention policy | ⚠️ | Configurable log rotation (default: 30 days) |
| GDPR data export | 🔄 | User data export endpoint |

## Deployment Security

### Docker Setup
```yaml
# Recommended docker-compose.yml security settings
services:
  server:
    # Run as non-root user
    user: "1000:1000"
    
    # Read-only root filesystem
    read_only: true
    
    # Mount needed directories
    tmpfs:
      - /tmp:size=64M,noexec,nosuid
    
    # Security options
    security_opt:
      - no-new-privileges:true
    
    # Network isolation
    networks:
      - vaporrmm-net

networks:
  vaporrmm-net:
    driver: bridge
```

### Environment Variables (`.env`)
```bash
# Required for production
DATABASE_URL=sqlite:///data/vaporrmm.db
JWT_SECRET=your-super-secret-jwt-key-min-32-chars
SERVER_PORT=8080

# Optional security settings
SERVER_CERT=/etc/ssl/certs/server.crt
SERVER_KEY=/etc/ssl/private/server.key
AGENT_MTLS=true
LOG_LEVEL=info
```

## Security Checklist for Deployment

### Pre-Deployment

- [ ] Generate strong `JWT_SECRET` (minimum 64 characters recommended)
- [ ] Configure TLS certificates for production use
- [ ] Set up log rotation and monitoring
- [ ] Review firewall rules (only ports 80/443 open to internet)
- [ ] Enable audit logging
- [ ] Test backup/recovery procedures

### Agent Deployment

- [ ] Use signed agent binaries
- [ ] Register agents with certificate-based auth
- [ ] Verify agent heartbeat encryption
- [ ] Set up agent monitoring alerts

## Vulnerability Reporting

If you discover a security vulnerability:

1. **Do NOT** create a public GitHub issue
2. Email details to: security@vaporrmm.com
3. Include:
   - Description of the vulnerability
   - Steps to reproduce
   - Potential impact
   - Suggested fix (if any)

We aim to acknowledge reports within 48 hours and provide regular updates on remediation progress.

## Security Updates & Alerts

Subscribe to security announcements for:
- New releases with security fixes
- Known vulnerabilities in dependencies
- Deployment recommendations

## Compliance Standards

vaporRMM is designed to support:

| Standard | Status | Notes |
|----------|--------|-------|
| SOC 2 Type II | 🔄 | Audit logs and access controls ready |
| HIPAA | 🔄 | Encryption at rest/transit configurable |
| GDPR | ✅ | Data export/delete endpoints available |

## Regular Security Tasks

### Daily
- Monitor audit logs for suspicious activity
- Check agent connection status

### Weekly
- Review failed login attempts
- Verify backup integrity
- Check for outdated dependencies

### Monthly
- Rotate secrets if required
- Audit user access permissions
- Review firewall rules

### Quarterly
- Penetration test production deployment
- Update all security certificates
- Review and update incident response plan

---

*Last updated: March 2026*