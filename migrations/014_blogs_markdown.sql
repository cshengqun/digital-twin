-- Blogs: Markdown posts. format = text (legacy plain text) | markdown.
ALTER TABLE journals ADD COLUMN IF NOT EXISTS format TEXT NOT NULL DEFAULT 'text';
