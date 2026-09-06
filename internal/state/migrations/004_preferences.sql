CREATE TABLE models (
    id INTEGER PRIMARY KEY,
    model TEXT NOT NULL UNIQUE,
    name TEXT NOT NULL,
    default_effort TEXT NOT NULL
);
INSERT INTO models (model, name, default_effort) VALUES
    ('gpt-5.6-sol', 'GPT-5.6 Sol', 'low'),
    ('gpt-5.6-luna', 'GPT-5.6 Luna', 'low'),
    ('gpt-5.6-terra', 'GPT-5.6 Terra', 'medium'),
    ('gpt-6-astra', 'GPT-6 Astra', 'medium');

CREATE TABLE preferences (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    default_model_id INTEGER NOT NULL REFERENCES models(id),
    default_image_profile TEXT NOT NULL
);
INSERT INTO preferences (id, default_model_id, default_image_profile)
    SELECT 1, id, 'generic' FROM models WHERE model = 'gpt-6-astra';
