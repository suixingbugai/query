## Grouping data with GROUP BY

Sometimes you want to break the data up into your own groups.

In this example we form groups based on the relation field.

<pre id="example">
SELECT relation, COUNT(*) AS count
    FROM tutorial
        GROUP BY relation
</pre>