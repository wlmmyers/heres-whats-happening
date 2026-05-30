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
