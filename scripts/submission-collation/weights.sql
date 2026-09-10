-- Run with mariadb --default-character-set=utf8mb4 --batch --skip-column-names.
-- The sequence table is virtual. This reads no application data and writes none.
SELECT seq, HEX(WEIGHT_STRING(
    CONVERT(CONVERT(UNHEX(LPAD(HEX(seq),8,'0')) USING utf32) USING utf8mb4)
    COLLATE utf8mb4_unicode_ci)),
    HEX(WEIGHT_STRING(CONVERT(CONVERT(UNHEX(LPAD(HEX(seq),8,'0')) USING utf32) USING utf8mb4) COLLATE utf8mb4_general_ci))
FROM seq_1_to_65535 WHERE seq NOT BETWEEN 55296 AND 57343;
