locals {
  inbound_domain   = "inbound.${var.domain_name}"
  ingest_recipient = "shows@inbound.${var.domain_name}"
  ses_inbound_host = "inbound-smtp.${var.aws_region}.amazonaws.com"
}

# Verify the receiving subdomain as an SES domain identity.
resource "aws_ses_domain_identity" "inbound" {
  domain = local.inbound_domain
}

resource "aws_ses_domain_dkim" "inbound" {
  domain = aws_ses_domain_identity.inbound.domain
}

# DKIM CNAMEs in the existing public hosted zone.
resource "aws_route53_record" "inbound_dkim" {
  count   = 3
  zone_id = data.aws_route53_zone.primary.zone_id
  name    = "${aws_ses_domain_dkim.inbound.dkim_tokens[count.index]}._domainkey.${local.inbound_domain}"
  type    = "CNAME"
  ttl     = 600
  records = ["${aws_ses_domain_dkim.inbound.dkim_tokens[count.index]}.dkim.amazonses.com"]
}

# MX so mail to *@inbound.<domain> is delivered to SES inbound.
resource "aws_route53_record" "inbound_mx" {
  zone_id = data.aws_route53_zone.primary.zone_id
  name    = local.inbound_domain
  type    = "MX"
  ttl     = 600
  records = ["10 ${local.ses_inbound_host}"]
}

# NOTE: AWS allows only ONE active receipt rule set per account. If the account
# already has an active rule set, do NOT apply aws_ses_active_receipt_rule_set —
# instead add the rule below to the existing set. Confirm with:
#   aws ses describe-active-receipt-rule-set
resource "aws_ses_receipt_rule_set" "main" {
  rule_set_name = "${var.app_name_prefix}-inbound"
}

resource "aws_ses_active_receipt_rule_set" "main" {
  rule_set_name = aws_ses_receipt_rule_set.main.rule_set_name
}

resource "aws_ses_receipt_rule" "store_to_s3" {
  name          = "${var.app_name_prefix}-store-newsletter"
  rule_set_name = aws_ses_receipt_rule_set.main.rule_set_name
  recipients    = [local.ingest_recipient]
  enabled       = true
  scan_enabled  = true # populates X-SES-Spam-Verdict / X-SES-Virus-Verdict

  s3_action {
    bucket_name       = aws_s3_bucket.inbound_email.bucket
    object_key_prefix = "raw/"
    position          = 1
  }

  depends_on = [aws_s3_bucket_policy.inbound_email]
}

# ---------------------------------------------------------------------------
# Outbound sending identity for the apex domain.
#
# Distinct from the inbound.<domain> identity above: that one receives
# newsletter mail, this one is the From: domain for transactional mail
# (email confirmation). Verifying the apex + DKIM is also what makes the
# SES production-access request credible.
# ---------------------------------------------------------------------------
resource "aws_ses_domain_identity" "apex" {
  domain = var.domain_name
}

resource "aws_ses_domain_dkim" "apex" {
  domain = aws_ses_domain_identity.apex.domain
}

resource "aws_route53_record" "apex_dkim" {
  count   = 3
  zone_id = data.aws_route53_zone.primary.zone_id
  name    = "${aws_ses_domain_dkim.apex.dkim_tokens[count.index]}._domainkey.${var.domain_name}"
  type    = "CNAME"
  ttl     = 600
  records = ["${aws_ses_domain_dkim.apex.dkim_tokens[count.index]}.dkim.amazonses.com"]
}

# SPF. NOTE: only ONE TXT record may carry v=spf1 for a name — a second one is
# a permanent error that fails SPF entirely. Verified before creating this that
# the zone has no apex TXT record; if one appears later, merge the mechanisms
# into it rather than adding another resource.
resource "aws_route53_record" "spf" {
  zone_id = data.aws_route53_zone.primary.zone_id
  name    = var.domain_name
  type    = "TXT"
  ttl     = 600
  records = ["v=spf1 include:amazonses.com ~all"]
}

# DMARC in monitor mode (p=none): report-only, so a misaligned message is
# never rejected while we establish a sending reputation.
resource "aws_route53_record" "dmarc" {
  zone_id = data.aws_route53_zone.primary.zone_id
  name    = "_dmarc.${var.domain_name}"
  type    = "TXT"
  ttl     = 600
  records = ["v=DMARC1; p=none; rua=mailto:dmarc@${var.domain_name}"]
}
