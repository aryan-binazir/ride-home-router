ALTER TABLE participants ADD COLUMN matched_address text NOT NULL DEFAULT '';
ALTER TABLE participants ADD COLUMN address_match text NOT NULL DEFAULT 'verified'
    CONSTRAINT participants_address_match_check CHECK (address_match IN ('verified', 'guessed', 'confirmed'));
ALTER TABLE drivers ADD COLUMN matched_address text NOT NULL DEFAULT '';
ALTER TABLE drivers ADD COLUMN address_match text NOT NULL DEFAULT 'verified'
    CONSTRAINT drivers_address_match_check CHECK (address_match IN ('verified', 'guessed', 'confirmed'));
