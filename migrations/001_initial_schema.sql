-- Initial schema for subscriptions table
-- Migration: 001_initial_schema

CREATE TABLE subscriptions (
    id STRING(255) NOT NULL,
    customer_id STRING(255) NOT NULL,
    plan_id STRING(255) NOT NULL,
    price_cents INT64 NOT NULL,
    status STRING(50) NOT NULL,
    start_date TIMESTAMP NOT NULL
) PRIMARY KEY (id);

CREATE INDEX idx_customer_id ON subscriptions(customer_id);
