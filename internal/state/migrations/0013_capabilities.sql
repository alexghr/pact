UPDATE executions SET settings = json_remove(json_set(settings, '$.capabilities',
    json_object('dind', json(CASE WHEN json_extract(settings, '$.dind') THEN 'true' ELSE 'false' END),
                'hostNetwork', json(CASE WHEN json_extract(settings, '$.hostNet') THEN 'true' ELSE 'false' END))),
    '$.dind', '$.hostNet', '$.prompted');
