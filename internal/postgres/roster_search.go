package postgres

const rosterSearchPredicate = `($1 = '' OR concat_ws(' ', name, address_name, address) ILIKE '%' || $1 || '%')`
