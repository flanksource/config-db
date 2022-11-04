-- +goose Up

ALTER TABLE config_relationships RENAME COLUMN property TO relation;
