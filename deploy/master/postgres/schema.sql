--
-- PostgreSQL database dump
--

\restrict i9R1lDTYAWWj7BccMQOPYwo1Vs0l3ZmScsvsW2askI1JngiB6vJkbhLiLFK57pg

-- Dumped from database version 14.23 (Ubuntu 14.23-0ubuntu0.22.04.1)
-- Dumped by pg_dump version 14.23 (Ubuntu 14.23-0ubuntu0.22.04.1)

SET statement_timeout = 0;
SET lock_timeout = 0;
SET idle_in_transaction_session_timeout = 0;
SET client_encoding = 'UTF8';
SET standard_conforming_strings = on;
SELECT pg_catalog.set_config('search_path', '', false);
SET check_function_bodies = false;
SET xmloption = content;
SET client_min_messages = warning;
SET row_security = off;

--
-- Name: public; Type: SCHEMA; Schema: -; Owner: -
--

CREATE SCHEMA IF NOT EXISTS public;


--
-- Name: SCHEMA public; Type: COMMENT; Schema: -; Owner: -
--

COMMENT ON SCHEMA public IS 'standard public schema';


--
-- Name: log_traffic_totals_change(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.log_traffic_totals_change() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    INSERT INTO traffic_totals_audit (
        client_name,

        old_rx,
        new_rx,

        old_tx,
        new_tx,

        old_total,
        new_total,

        delta_total,

        application_name,
        db_user,
        client_addr,
        query_text
    )
    VALUES (
        NEW.client_name,

        OLD.rx_bytes,
        NEW.rx_bytes,

        OLD.tx_bytes,
        NEW.tx_bytes,

        OLD.total_bytes,
        NEW.total_bytes,

        NEW.total_bytes - OLD.total_bytes,

        current_setting('application_name', true),
        current_user,
        inet_client_addr(),
        current_query()
    );

    RETURN NEW;
END;
$$;


SET default_tablespace = '';

SET default_table_access_method = heap;

--
-- Name: active_sessions; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.active_sessions (
    source_id text NOT NULL,
    client_name text NOT NULL,
    session_fingerprint text NOT NULL,
    real_address text,
    virtual_ip text,
    connected_since timestamp with time zone,
    last_seen timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: clients; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.clients (
    client_name text NOT NULL,
    display_name text,
    traffic_limit_bytes bigint DEFAULT 0 NOT NULL,
    expire_at timestamp with time zone,
    max_connections integer DEFAULT 1 NOT NULL,
    is_enabled boolean DEFAULT true NOT NULL,
    is_blocked boolean DEFAULT false NOT NULL,
    block_reason text,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    subscription_token text,
    subscription_url text,
    nodes jsonb DEFAULT '[]'::jsonb NOT NULL,
    status text,
    cipher text,
    last_seen timestamp with time zone,
    last_connection timestamp with time zone
);


--
-- Name: traffic_totals; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.traffic_totals (
    client_name text NOT NULL,
    rx_bytes bigint DEFAULT 0 NOT NULL,
    tx_bytes bigint DEFAULT 0 NOT NULL,
    total_bytes bigint DEFAULT 0 NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: enforcement_candidates; Type: VIEW; Schema: public; Owner: -
--

CREATE VIEW public.enforcement_candidates AS
 SELECT c.client_name,
    c.traffic_limit_bytes,
    COALESCE(t.total_bytes, (0)::bigint) AS used_bytes,
    c.expire_at,
    c.max_connections,
    c.is_blocked,
        CASE
            WHEN ((c.expire_at IS NOT NULL) AND (c.expire_at <= now())) THEN 'expired'::text
            WHEN ((c.traffic_limit_bytes > 0) AND (COALESCE(t.total_bytes, (0)::bigint) >= c.traffic_limit_bytes)) THEN 'traffic_limit'::text
            ELSE NULL::text
        END AS reason
   FROM (public.clients c
     LEFT JOIN public.traffic_totals t ON ((t.client_name = c.client_name)))
  WHERE (c.is_enabled = true);


--
-- Name: enforcement_events; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.enforcement_events (
    id bigint NOT NULL,
    client_name text NOT NULL,
    event_type text NOT NULL,
    source_id text,
    reason text,
    details jsonb DEFAULT '{}'::jsonb NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: enforcement_events_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.enforcement_events_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: enforcement_events_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.enforcement_events_id_seq OWNED BY public.enforcement_events.id;


--
-- Name: quota_blocks; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.quota_blocks (
    client_name text NOT NULL,
    blocked boolean DEFAULT false NOT NULL,
    reason text,
    details jsonb DEFAULT '{}'::jsonb NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: traffic_sources; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.traffic_sources (
    source_id text NOT NULL,
    client_name text NOT NULL,
    session_fingerprint text DEFAULT ''::text NOT NULL,
    rx_bytes bigint DEFAULT 0 NOT NULL,
    tx_bytes bigint DEFAULT 0 NOT NULL,
    total_bytes bigint DEFAULT 0 NOT NULL,
    connected boolean DEFAULT false NOT NULL,
    real_address text,
    virtual_ip text,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: traffic_totals_audit; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.traffic_totals_audit (
    id bigint NOT NULL,
    changed_at timestamp with time zone DEFAULT now() NOT NULL,
    client_name text NOT NULL,
    old_rx bigint,
    new_rx bigint,
    old_tx bigint,
    new_tx bigint,
    old_total bigint,
    new_total bigint,
    delta_total bigint,
    application_name text,
    db_user text,
    client_addr inet,
    query_text text
);


--
-- Name: traffic_totals_audit_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.traffic_totals_audit_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: traffic_totals_audit_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.traffic_totals_audit_id_seq OWNED BY public.traffic_totals_audit.id;


--
-- Name: enforcement_events id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.enforcement_events ALTER COLUMN id SET DEFAULT nextval('public.enforcement_events_id_seq'::regclass);


--
-- Name: traffic_totals_audit id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.traffic_totals_audit ALTER COLUMN id SET DEFAULT nextval('public.traffic_totals_audit_id_seq'::regclass);


--
-- Name: active_sessions active_sessions_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.active_sessions
    ADD CONSTRAINT active_sessions_pkey PRIMARY KEY (source_id, client_name, session_fingerprint);


--
-- Name: clients clients_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.clients
    ADD CONSTRAINT clients_pkey PRIMARY KEY (client_name);


--
-- Name: enforcement_events enforcement_events_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.enforcement_events
    ADD CONSTRAINT enforcement_events_pkey PRIMARY KEY (id);


--
-- Name: quota_blocks quota_blocks_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.quota_blocks
    ADD CONSTRAINT quota_blocks_pkey PRIMARY KEY (client_name);


--
-- Name: traffic_sources traffic_sources_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.traffic_sources
    ADD CONSTRAINT traffic_sources_pkey PRIMARY KEY (source_id, client_name);


--
-- Name: traffic_totals_audit traffic_totals_audit_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.traffic_totals_audit
    ADD CONSTRAINT traffic_totals_audit_pkey PRIMARY KEY (id);


--
-- Name: traffic_totals traffic_totals_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.traffic_totals
    ADD CONSTRAINT traffic_totals_pkey PRIMARY KEY (client_name);


--
-- Name: idx_active_sessions_client; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_active_sessions_client ON public.active_sessions USING btree (client_name);


--
-- Name: idx_active_sessions_seen; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_active_sessions_seen ON public.active_sessions USING btree (last_seen);


--
-- Name: idx_clients_blocked; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_clients_blocked ON public.clients USING btree (is_blocked);


--
-- Name: idx_clients_expire_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_clients_expire_at ON public.clients USING btree (expire_at);


--
-- Name: idx_clients_expire_block; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_clients_expire_block ON public.clients USING btree (expire_at, is_blocked);


--
-- Name: idx_clients_status; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_clients_status ON public.clients USING btree (status);


--
-- Name: idx_clients_subscription_token; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_clients_subscription_token ON public.clients USING btree (subscription_token);


--
-- Name: idx_events_client_time; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_events_client_time ON public.enforcement_events USING btree (client_name, created_at DESC);


--
-- Name: idx_traffic_sources_client; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_traffic_sources_client ON public.traffic_sources USING btree (client_name);


--
-- Name: idx_traffic_totals_client_total; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_traffic_totals_client_total ON public.traffic_totals USING btree (client_name, total_bytes);


--
-- Name: idx_traffic_totals_total; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_traffic_totals_total ON public.traffic_totals USING btree (total_bytes);


--
-- Name: traffic_totals trg_traffic_totals_audit; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER trg_traffic_totals_audit AFTER UPDATE ON public.traffic_totals FOR EACH ROW WHEN (((old.rx_bytes IS DISTINCT FROM new.rx_bytes) OR (old.tx_bytes IS DISTINCT FROM new.tx_bytes) OR (old.total_bytes IS DISTINCT FROM new.total_bytes))) EXECUTE FUNCTION public.log_traffic_totals_change();


--
-- Name: active_sessions active_sessions_client_name_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.active_sessions
    ADD CONSTRAINT active_sessions_client_name_fkey FOREIGN KEY (client_name) REFERENCES public.clients(client_name) ON DELETE CASCADE;


--
-- Name: quota_blocks quota_blocks_client_name_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.quota_blocks
    ADD CONSTRAINT quota_blocks_client_name_fkey FOREIGN KEY (client_name) REFERENCES public.clients(client_name) ON DELETE CASCADE;


--
-- Name: traffic_sources traffic_sources_client_name_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.traffic_sources
    ADD CONSTRAINT traffic_sources_client_name_fkey FOREIGN KEY (client_name) REFERENCES public.clients(client_name) ON DELETE CASCADE;


--
-- Name: traffic_totals traffic_totals_client_name_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.traffic_totals
    ADD CONSTRAINT traffic_totals_client_name_fkey FOREIGN KEY (client_name) REFERENCES public.clients(client_name) ON DELETE CASCADE;


--
-- PostgreSQL database dump complete
--

\unrestrict i9R1lDTYAWWj7BccMQOPYwo1Vs0l3ZmScsvsW2askI1JngiB6vJkbhLiLFK57pg
