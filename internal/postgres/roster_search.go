package postgres

// rosterSearchPredicate matches the searchable fields shared by both rosters.
const rosterSearchPredicate = `($1 = '' OR concat_ws(' ', name, address_name, address) ILIKE '%' || $1 || '%')`
