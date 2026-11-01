-- DESIGN PROTOTYPE ONLY. psql variables are generated exact identifiers from a private manifest.
-- Use only the dedicated local QA DB after checking its OID. Do not replay against shared deployments.
-- Password variables are passed only in memory; never print psql command/input or enable query logs.
CREATE ROLE :"app_owner" NOLOGIN NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS;
CREATE ROLE :"app_runtime" LOGIN NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS PASSWORD :'app_password';
CREATE ROLE :"app_projector" LOGIN NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS PASSWORD :'projector_password';
CREATE ROLE :"mock_owner" NOLOGIN NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS;
CREATE ROLE :"mock_runtime" LOGIN NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS PASSWORD :'mock_password';
CREATE ROLE :"receiver_owner" NOLOGIN NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS;
CREATE ROLE :"receiver_runtime" LOGIN NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS PASSWORD :'receiver_password';
CREATE SCHEMA :"app_schema" AUTHORIZATION :"app_owner";
CREATE SCHEMA :"mock_schema" AUTHORIZATION :"mock_owner";
CREATE SCHEMA :"receiver_schema" AUTHORIZATION :"receiver_owner";
REVOKE ALL ON SCHEMA :"app_schema",:"mock_schema",:"receiver_schema" FROM PUBLIC;
-- No role membership is granted. No REVOKE on global database/public schema or other QA schema.
