#!/bin/sh

count=10000

while [ $count != 0 ]
do
    echo "This is the count: $count"
    psql -d "postgres" -c "INSERT INTO entries (lang, content, initial, tokens, phones) VALUES ('kannada', 'ಕನ್ನಡ', 'ಕ', TO_TSVECTOR('ಕನ್ನಡ'), '{kannaḍa}');"
    let "count = $count - 1"
done
