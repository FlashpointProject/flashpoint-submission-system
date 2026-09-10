.PHONY: test
test:
	bash ./run.sh

.PHONY: test-postgres test-both
test-postgres:
	SUBMISSION_DB_ENGINE=postgres bash ./run.sh

test-both:
	SUBMISSION_DB_ENGINE=mariadb bash ./run.sh
	SUBMISSION_DB_ENGINE=postgres bash ./run.sh
