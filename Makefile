.PHONY: install serve

install:
	python -m pip install -r requirements.txt

serve: install
	python -m mkdocs serve -f ../mkdocs.yml
